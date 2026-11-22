package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
)

// truncatedKey is the pagination field that says the result size budget, not
// limit, ended a page.
const truncatedKey = "truncated"

// listShape locates the items of a list tool's page.
type listShape struct {
	// itemsPath is the object keys from the payload's top level to its items array.
	itemsPath []string
	// windowed is true for a page that reports the window it queried, which
	// the next page must repeat.
	windowed bool
	// offset and limit, with the total member beside the items, build the
	// pagination object a cut adds to a payload without one (SigNoz's
	// dashboard list).
	offset, limit int
}

var (
	dataItems         = listShape{itemsPath: []string{"data"}}
	windowedDataItems = listShape{itemsPath: []string{"data"}, windowed: true}
)

// listResult is the result of a list tool that pages with offset, for a
// code-controlled envelope whose items are at shape.itemsPath and whose
// pagination object is top-level.
func (h *Handler) listResult(ctx context.Context, toolName string, payload []byte, shape listShape, limitClamped bool, notes ...string) *mcp.CallToolResult {
	return h.budgetedListResult(ctx, toolName, payload, shape, withClampNote(limitClamped, notes)...)
}

// unbudgetedListResult is for a list SigNoz returns in no fixed order. After a
// cut the caller continues from an offset, where an item that moved between
// the two calls would come back twice or not at all.
func (h *Handler) unbudgetedListResult(ctx context.Context, payload []byte, limitClamped bool, notes ...string) *mcp.CallToolResult {
	return structuredResultWithNotes(ctx, h.logger, payload, withClampNote(limitClamped, notes)...)
}

// withClampNote puts a clamp note ahead of the tool's own notes when the
// requested per-page limit was clamped to paginate.MaxLimit.
func withClampNote(limitClamped bool, notes []string) []string {
	if !limitClamped {
		return notes
	}
	return append([]string{fmt.Sprintf(
		"note: limit clamped to %d per page to bound server memory; use \"offset\" to page through more results.",
		paginate.MaxLimit)}, notes...)
}

// budgetedListResult cuts a page over the result size budget to its leading
// whole items that fit. Items are never shortened, because a caller may write
// one back through an update tool, so the first item always stays whole.
func (h *Handler) budgetedListResult(ctx context.Context, toolName string, payload []byte, shape listShape, notes ...string) *mcp.CallToolResult {
	page := &listPage{h: h, budget: h.defaultResultBytes(), payload: payload, shape: shape, notes: notes}
	return deliverFitted(ctx, func(extra []string) (*mcp.CallToolResult, pageOutcome) {
		return page.fit(ctx, extra)
	}, func(outcome pageOutcome) {
		if page.splitErr != nil {
			h.warnLogger().WarnContext(ctx, "list result left unbounded because its items could not be paged",
				slog.String("gen_ai.tool.name", toolName),
				logpkg.ErrAttr(page.splitErr))
			h.countBudgetEvent(ctx, toolName, budgetEventFailOpen)
			return
		}
		items := 0
		if page.split != nil {
			items = len(page.split.items)
		}
		h.recordBudget(ctx, toolName, page.budget, items, outcome)
	})
}

// listPage is one page of a list tool, split at its items only when the page
// is over budget.
type listPage struct {
	h        *Handler
	budget   int
	payload  []byte
	shape    listShape
	notes    []string
	split    *listSplit
	splitErr error
}

// fit returns the result whose text blocks, extra notes included, fit the
// budget: the whole page when it fits, else the longest prefix of whole items
// that does, else the first item alone.
func (p *listPage) fit(ctx context.Context, extra []string) (*mcp.CallToolResult, pageOutcome) {
	notes := append(append([]string(nil), p.notes...), extra...)
	whole := structuredResultWithNotes(ctx, p.h.logger, p.payload, notes...)
	size := textBytes(whole)
	if size <= p.budget {
		return whole, pageOutcome{textBytes: size}
	}
	if p.split == nil && p.splitErr == nil {
		p.split, p.splitErr = splitList(p.payload, p.shape)
	}
	if p.splitErr != nil {
		return whole, pageOutcome{textBytes: size}
	}
	total := len(p.split.items)
	if total <= 1 {
		return whole, pageOutcome{textBytes: size, overBudget: true}
	}

	build := func(keep int) (*mcp.CallToolResult, int) {
		cut := append(append([]string(nil), p.notes...), p.split.note(keep, p.budget))
		result := structuredResultWithNotes(ctx, p.h.logger, p.split.render(keep, p.budget), append(cut, extra...)...)
		return result, textBytes(result)
	}
	result, size := build(1)
	if size > p.budget {
		return result, pageOutcome{dropped: total - 1, textBytes: size, overBudget: true}
	}
	estimate, used := 1, size
	for _, item := range p.split.items[1 : total-1] {
		if used += len(item) + 1; used > p.budget {
			break
		}
		estimate++
	}
	// Block 0 is re-encoded with the notes (\u003c becomes <), so the estimate
	// can be far off. Probe it and its neighbor, then bisect: the first low
	// items fit, and the first high do not or are the whole page.
	low, high := 1, total
	for probe, tries := estimate, 0; low+1 < high; tries++ {
		probe = min(max(probe, low+1), high-1)
		next, nextSize := build(probe)
		fits := nextSize <= p.budget
		if fits {
			low, result, size = probe, next, nextSize
		} else {
			high = probe
		}
		switch {
		case tries == 0 && fits:
			probe = low + 1
		case tries == 0:
			probe = high - 1
		default:
			probe = low + (high-low)/2
		}
	}
	return result, pageOutcome{dropped: total - low, textBytes: size}
}

// listSplit is a list page split at its items, so a cut can keep a prefix of
// them and re-encode the objects around them. Items keep their bytes.
type listSplit struct {
	levels []jsonLevel // the objects from the top level down to the one holding the items
	items  []json.RawMessage
	// pagination is the page's pagination object, which a cut edits; it is
	// the paginationAt member of the top level, or added when that is -1.
	pagination    []jsonMember
	paginationAt  int
	offset, limit int
	windowed      bool
}

// jsonLevel is one object on the way to a page's items, with the index of the
// member leading on.
type jsonLevel struct {
	members []jsonMember
	at      int
}

// splitList splits payload at shape's items. It fails on any shape it cannot
// walk and on a pagination object without an integer offset and limit.
func splitList(payload []byte, shape listShape) (*listSplit, error) {
	split := &listSplit{paginationAt: -1, windowed: shape.windowed}
	raw := json.RawMessage(payload)
	for depth, key := range shape.itemsPath {
		members, ok := decodeObject(raw)
		if !ok {
			return nil, fmt.Errorf("%s is not a JSON object", jsonPathName(shape.itemsPath[:depth]))
		}
		at := memberIndex(members, key)
		if at < 0 {
			return nil, fmt.Errorf("%s has no %q member", jsonPathName(shape.itemsPath[:depth]), key)
		}
		raw = members[at].value
		split.levels = append(split.levels, jsonLevel{members: members, at: at})
	}
	items, ok := decodeArrayOrNull(raw)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", jsonPathName(shape.itemsPath))
	}
	split.items = items

	top := split.levels[0].members
	if split.paginationAt = memberIndex(top, paginationKey); split.paginationAt >= 0 {
		if split.pagination, ok = decodeObject(top[split.paginationAt].value); !ok {
			return nil, errors.New("pagination is not an object")
		}
	} else {
		holder := split.levels[len(split.levels)-1].members
		// A pointer tells null from a number: null decodes into an int as 0.
		var total *int
		at := memberIndex(holder, "total")
		if at < 0 || json.Unmarshal(holder[at].value, &total) != nil || total == nil {
			return nil, errors.New("payload has neither a pagination object nor an integer total beside its items")
		}
		encoded, _ := json.Marshal(paginate.NewMetadata(*total, shape.offset, shape.limit)) // a struct of ints and bools always encodes
		split.pagination, _ = decodeObject(encoded)
	}
	for _, field := range []struct {
		key   string
		value *int
	}{{"offset", &split.offset}, {"limit", &split.limit}} {
		at := memberIndex(split.pagination, field.key)
		if at < 0 || json.Unmarshal(split.pagination[at].value, field.value) != nil {
			return nil, fmt.Errorf("pagination has no integer %s", field.key)
		}
	}
	return split, nil
}

func jsonPathName(path []string) string {
	if len(path) == 0 {
		return "the payload"
	}
	return strings.Join(path, ".")
}

// render encodes the page with its first keep items and its pagination object
// marked as ended by the budget after them.
func (s *listSplit) render(keep, budget int) []byte {
	value := encodeArray(s.items[:keep])
	for depth := len(s.levels) - 1; depth >= 0; depth-- {
		level := s.levels[depth]
		members := append([]jsonMember(nil), level.members...)
		members[level.at].value = value
		if depth == 0 {
			members = setMember(members, s.paginationAt, paginationKey, encodeObject(s.cutPagination(keep, budget)))
		}
		value = encodeObject(members)
	}
	return value
}

// cutPagination is the pagination object of a page the budget ended after
// keep items. total is left as it was: it counts the list, not the page.
func (s *listSplit) cutPagination(keep, budget int) []jsonMember {
	members := append([]jsonMember(nil), s.pagination...)
	set := func(key, value string) {
		members = setMember(members, memberIndex(members, key), key, json.RawMessage(value))
	}
	set("hasMore", "true")
	set("nextOffset", strconv.Itoa(s.offset+keep))
	if memberIndex(members, "count") >= 0 {
		set("count", strconv.Itoa(keep))
	}
	set(truncatedKey, "true")
	set(maxResultBytesParam, strconv.Itoa(budget))
	return members
}

// setMember replaces the member at index at, or appends key when at is -1.
func setMember(members []jsonMember, at int, key string, value json.RawMessage) []jsonMember {
	if at >= 0 {
		members[at].value = value
		return members
	}
	return append(members, jsonMember{key: key, value: value})
}

// note says the budget ended the page after keep items and where the next
// page starts.
func (s *listSplit) note(keep, budget int) string {
	note := fmt.Sprintf(
		"note: returned %d items (limit %d); the result size budget of %d bytes ended this page, so more results exist (hasMore=true). Fetch the next page with offset=%d",
		keep, s.limit, budget, s.offset+keep)
	if s.windowed {
		note += ", and pass window.startMs and window.endMs as start and end so the page does not shift"
	}
	return note + "."
}
