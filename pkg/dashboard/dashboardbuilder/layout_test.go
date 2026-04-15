package dashboardbuilder

import "testing"

func TestComputeAutoLayout_EmptyWidgets(t *testing.T) {
	layout := ComputeAutoLayout(nil)
	if len(layout) != 0 {
		t.Fatalf("expected empty layout, got %d items", len(layout))
	}
}

func TestComputeAutoLayout_SingleWidget(t *testing.T) {
	widgets := []WidgetOrRow{
		{ID: "w1", PanelTypes: PanelTypeGraph},
	}
	layout := ComputeAutoLayout(widgets)
	if len(layout) != 1 {
		t.Fatalf("expected 1 layout item, got %d", len(layout))
	}
	assertLayout(t, layout[0], "w1", 0, 0, DefaultWidgetWidth, DefaultWidgetHeight)
}

func TestComputeAutoLayout_TwoWidgetsFitInOneRow(t *testing.T) {
	widgets := []WidgetOrRow{
		{ID: "w1", PanelTypes: PanelTypeGraph},
		{ID: "w2", PanelTypes: PanelTypeGraph},
	}
	layout := ComputeAutoLayout(widgets)
	if len(layout) != 2 {
		t.Fatalf("expected 2 layout items, got %d", len(layout))
	}
	// First at (0,0), second at (6,0) since 6+6=12 fits.
	assertLayout(t, layout[0], "w1", 0, 0, DefaultWidgetWidth, DefaultWidgetHeight)
	assertLayout(t, layout[1], "w2", 6, 0, DefaultWidgetWidth, DefaultWidgetHeight)
}

func TestComputeAutoLayout_ThreeWidgetsWrapToNextRow(t *testing.T) {
	widgets := []WidgetOrRow{
		{ID: "w1", PanelTypes: PanelTypeGraph},
		{ID: "w2", PanelTypes: PanelTypeGraph},
		{ID: "w3", PanelTypes: PanelTypeGraph},
	}
	layout := ComputeAutoLayout(widgets)
	if len(layout) != 3 {
		t.Fatalf("expected 3 layout items, got %d", len(layout))
	}
	assertLayout(t, layout[0], "w1", 0, 0, DefaultWidgetWidth, DefaultWidgetHeight)
	assertLayout(t, layout[1], "w2", 6, 0, DefaultWidgetWidth, DefaultWidgetHeight)
	// Third wraps to next row.
	assertLayout(t, layout[2], "w3", 0, 6, DefaultWidgetWidth, DefaultWidgetHeight)
}

func TestComputeAutoLayout_RowWidget(t *testing.T) {
	widgets := []WidgetOrRow{
		{ID: "row1", PanelTypes: PanelTypeRow},
		{ID: "w1", PanelTypes: PanelTypeGraph},
	}
	layout := ComputeAutoLayout(widgets)
	if len(layout) != 2 {
		t.Fatalf("expected 2 layout items, got %d", len(layout))
	}
	assertLayout(t, layout[0], "row1", 0, 0, RowWidth, RowHeight)
	assertLayout(t, layout[1], "w1", 0, 1, DefaultWidgetWidth, DefaultWidgetHeight)
}

func TestComputeAutoLayout_RowBetweenWidgets(t *testing.T) {
	widgets := []WidgetOrRow{
		{ID: "w1", PanelTypes: PanelTypeGraph},
		{ID: "row1", PanelTypes: PanelTypeRow},
		{ID: "w2", PanelTypes: PanelTypeGraph},
	}
	layout := ComputeAutoLayout(widgets)
	if len(layout) != 3 {
		t.Fatalf("expected 3 layout items, got %d", len(layout))
	}
	assertLayout(t, layout[0], "w1", 0, 0, DefaultWidgetWidth, DefaultWidgetHeight)
	assertLayout(t, layout[1], "row1", 0, 6, RowWidth, RowHeight)
	assertLayout(t, layout[2], "w2", 0, 7, DefaultWidgetWidth, DefaultWidgetHeight)
}

func assertLayout(t *testing.T, item LayoutItem, id string, x, y, w, h int) {
	t.Helper()
	if item.I != id {
		t.Errorf("expected id %q, got %q", id, item.I)
	}
	if item.X != x {
		t.Errorf("layout %q: expected x=%d, got %d", id, x, item.X)
	}
	if item.Y != y {
		t.Errorf("layout %q: expected y=%d, got %d", id, y, item.Y)
	}
	if item.W != w {
		t.Errorf("layout %q: expected w=%d, got %d", id, w, item.W)
	}
	if item.H != h {
		t.Errorf("layout %q: expected h=%d, got %d", id, h, item.H)
	}
}
