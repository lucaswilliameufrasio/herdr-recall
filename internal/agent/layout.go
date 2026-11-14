package agent

import (
	"errors"

	"herdr-recall/internal/contract"
)

type cellRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func (r cellRect) area() int {
	return r.Width * r.Height
}

func buildLayouts(snap herdrSnapshot) ([]contract.Layout, error) {
	if len(snap.Panes) == 0 {
		return nil, nil
	}
	if len(snap.Layouts) == 0 {
		return nil, errors.New("divisão ausente")
	}
	workspaces := map[string]herdrWorkspace{}
	for _, workspace := range snap.Workspaces {
		workspaces[workspace.ID] = workspace
	}
	tabs := map[string]herdrTab{}
	for _, tab := range snap.Tabs {
		tabs[tab.ID] = tab
	}
	byTab := map[string]herdrLayout{}
	for _, layout := range snap.Layouts {
		byTab[layout.TabID] = layout
	}
	layouts := make([]contract.Layout, 0, len(byTab))
	covered := map[string]struct{}{}
	for _, pane := range snap.Panes {
		layout, ok := byTab[pane.TabID]
		if !ok {
			return nil, errors.New("aba sem divisão")
		}
		if _, done := covered[pane.TabID]; done {
			continue
		}
		covered[pane.TabID] = struct{}{}
		tab := tabs[pane.TabID]
		workspace := workspaces[tab.WorkspaceID]
		if layout.WorkspaceID != "" && layout.WorkspaceID != tab.WorkspaceID {
			return nil, errors.New("divisão de outro workspace")
		}
		root, err := buildNode(layout.Area.toRect(), layout.Panes, layout.Splits)
		if err != nil {
			return nil, err
		}
		layouts = append(layouts, contract.Layout{
			WorkspaceID:     tab.WorkspaceID,
			TabID:           pane.TabID,
			WorkspaceNumber: workspace.Number,
			TabNumber:       tab.Number,
			Zoomed:          layout.Zoomed,
			Root:            root,
		})
	}
	return layouts, nil
}

func buildNode(rect cellRect, panes []herdrLayoutPane, splits []herdrLayoutSplit) (*contract.Node, error) {
	if rect.Width <= 0 || rect.Height <= 0 {
		return nil, errors.New("área vazia")
	}
	var paneHits []herdrLayoutPane
	for _, pane := range panes {
		if rectEq(pane.Rect.toRect(), rect) {
			paneHits = append(paneHits, pane)
		}
	}
	var splitHits []herdrLayoutSplit
	for _, split := range splits {
		if rectEq(split.Rect.toRect(), rect) {
			splitHits = append(splitHits, split)
		}
	}
	if len(paneHits) == 1 && len(splitHits) == 0 {
		if paneHits[0].PaneID == "" {
			return nil, errors.New("pane sem id")
		}
		return &contract.Node{Type: contract.NodePane, PaneID: paneHits[0].PaneID}, nil
	}
	if len(splitHits) != 1 {
		return nil, errors.New("divisão ilegível")
	}
	split := splitHits[0]
	if split.Direction != contract.SplitRight && split.Direction != contract.SplitDown {
		return nil, errors.New("direção inválida")
	}
	if split.Ratio <= 0 || split.Ratio >= 1 {
		return nil, errors.New("proporção inválida")
	}
	var inner []herdrLayoutSplit
	for _, candidate := range splits {
		candidateRect := candidate.Rect.toRect()
		if contains(rect, candidateRect) && !rectEq(rect, candidateRect) {
			inner = append(inner, candidate)
		}
	}
	type child struct {
		rect cellRect
	}
	var kids []child
	for _, candidate := range inner {
		candidateRect := candidate.Rect.toRect()
		if !covered(candidateRect, inner) {
			kids = append(kids, child{rect: candidateRect})
		}
	}
	for _, pane := range panes {
		paneRect := pane.Rect.toRect()
		if contains(rect, paneRect) && !rectEq(rect, paneRect) && !covered(paneRect, inner) {
			kids = append(kids, child{rect: paneRect})
		}
	}
	if len(kids) != 2 {
		return nil, errors.New("divisão ilegível")
	}
	if split.Direction == contract.SplitRight {
		if kids[1].rect.X < kids[0].rect.X || (kids[1].rect.X == kids[0].rect.X && kids[1].rect.Y < kids[0].rect.Y) {
			kids[0], kids[1] = kids[1], kids[0]
		}
	} else if kids[1].rect.Y < kids[0].rect.Y || (kids[1].rect.Y == kids[0].rect.Y && kids[1].rect.X < kids[0].rect.X) {
		kids[0], kids[1] = kids[1], kids[0]
	}
	first, err := buildNode(kids[0].rect, panes, splits)
	if err != nil {
		return nil, err
	}
	second, err := buildNode(kids[1].rect, panes, splits)
	if err != nil {
		return nil, err
	}
	return &contract.Node{
		Type:      contract.NodeSplit,
		Direction: split.Direction,
		Ratio:     split.Ratio,
		First:     first,
		Second:    second,
	}, nil
}

func covered(rect cellRect, inner []herdrLayoutSplit) bool {
	for _, split := range inner {
		splitRect := split.Rect.toRect()
		if contains(splitRect, rect) && !rectEq(splitRect, rect) {
			return true
		}
	}
	return false
}

func contains(outer cellRect, inner cellRect) bool {
	const tol = 1
	return inner.X+tol >= outer.X &&
		inner.Y+tol >= outer.Y &&
		inner.X+inner.Width <= outer.X+outer.Width+tol &&
		inner.Y+inner.Height <= outer.Y+outer.Height+tol
}

func rectEq(left cellRect, right cellRect) bool {
	return absInt(left.X-right.X) <= 1 &&
		absInt(left.Y-right.Y) <= 1 &&
		absInt(left.Width-right.Width) <= 1 &&
		absInt(left.Height-right.Height) <= 1
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
