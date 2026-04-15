package dashboardbuilder

// ComputeAutoLayout generates a layout for widgets on a 12-column grid.
// It replicates the frontend's placeWidgetAtBottom logic: widgets are placed
// left-to-right, top-to-bottom with default size 6x6. Rows span full width (12x1).
func ComputeAutoLayout(widgets []WidgetOrRow) []LayoutItem {
	layout := make([]LayoutItem, 0, len(widgets))
	for _, w := range widgets {
		if w.PanelTypes == PanelTypeRow {
			layout = append(layout, placeRow(w.ID, layout))
		} else {
			layout = append(layout, placeWidgetAtBottom(w.ID, layout))
		}
	}
	return layout
}

func placeWidgetAtBottom(widgetID string, existing []LayoutItem) LayoutItem {
	if len(existing) == 0 {
		return LayoutItem{I: widgetID, X: 0, Y: 0, W: DefaultWidgetWidth, H: DefaultWidgetHeight}
	}

	maxY := maxBottom(existing)

	// Find items whose bottom edge equals maxY (last row of items).
	maxX := 0
	for _, item := range existing {
		if item.Y+item.H == maxY {
			right := item.X + item.W
			if right > maxX {
				maxX = right
			}
		}
	}

	// If there's space on the current last row, place beside existing items.
	if maxX+DefaultWidgetWidth <= GridColumns {
		return LayoutItem{
			I: widgetID,
			X: maxX,
			Y: maxY - DefaultWidgetHeight,
			W: DefaultWidgetWidth,
			H: DefaultWidgetHeight,
		}
	}

	// Otherwise, start a new row below.
	return LayoutItem{
		I: widgetID,
		X: 0,
		Y: maxY,
		W: DefaultWidgetWidth,
		H: DefaultWidgetHeight,
	}
}

func placeRow(rowID string, existing []LayoutItem) LayoutItem {
	return LayoutItem{
		I: rowID,
		X: 0,
		Y: maxBottom(existing),
		W: RowWidth,
		H: RowHeight,
	}
}

func maxBottom(items []LayoutItem) int {
	m := 0
	for _, item := range items {
		if bottom := item.Y + item.H; bottom > m {
			m = bottom
		}
	}
	return m
}
