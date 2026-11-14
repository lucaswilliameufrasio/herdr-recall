package agent

import (
	"testing"

	"herdr-recall/internal/contract"
)

func TestParseSnapshotNestsThreeColumns(t *testing.T) {
	raw := []byte(`{"result":{"snapshot":{
		"workspaces":[{"workspace_id":"w1","label":"mesa","number":1}],
		"tabs":[{"tab_id":"t1","workspace_id":"w1","label":"aba","number":1}],
		"panes":[
			{"pane_id":"p0","workspace_id":"w1","tab_id":"t1","cwd":"/tmp/a"},
			{"pane_id":"p1","workspace_id":"w1","tab_id":"t1","cwd":"/tmp/b"},
			{"pane_id":"p2","workspace_id":"w1","tab_id":"t1","cwd":"/tmp/c"}
		],
		"layouts":[{
			"workspace_id":"w1","tab_id":"t1","zoomed":false,
			"area":{"x":0,"y":0,"width":30,"height":10},
			"panes":[
				{"pane_id":"p0","rect":{"x":0,"y":0,"width":10,"height":10}},
				{"pane_id":"p1","rect":{"x":10,"y":0,"width":10,"height":10}},
				{"pane_id":"p2","rect":{"x":20,"y":0,"width":10,"height":10}}
			],
			"splits":[
				{"direction":"right","ratio":0.3333333333333333,"rect":{"x":0,"y":0,"width":30,"height":10}},
				{"direction":"right","ratio":0.5,"rect":{"x":10,"y":0,"width":20,"height":10}}
			]
		}]
	}}}`)
	_, layouts, err := ParseSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(layouts) != 1 {
		t.Fatalf("layouts: %d", len(layouts))
	}
	root := layouts[0].Root
	if root.Type != contract.NodeSplit || root.First.PaneID != "p0" || root.Second.Type != contract.NodeSplit {
		t.Fatalf("raiz: %+v", root)
	}
	if root.Second.First.PaneID != "p1" || root.Second.Second.PaneID != "p2" || root.Second.Ratio != 0.5 {
		t.Fatalf("lado: %+v", root.Second)
	}
}

func TestParseSnapshotRejectsABrokenSplit(t *testing.T) {
	raw := []byte(`{"result":{"snapshot":{
		"workspaces":[{"workspace_id":"w1","label":"mesa","number":1}],
		"tabs":[{"tab_id":"t1","workspace_id":"w1","label":"aba","number":1}],
		"panes":[{"pane_id":"p0","workspace_id":"w1","tab_id":"t1","cwd":"/tmp/a"}],
		"layouts":[{
			"workspace_id":"w1","tab_id":"t1",
			"area":{"x":0,"y":0,"width":20,"height":10},
			"panes":[{"pane_id":"p0","rect":{"x":0,"y":0,"width":10,"height":10}}],
			"splits":[{"direction":"right","ratio":0.5,"rect":{"x":0,"y":0,"width":20,"height":10}}]
		}]
	}}}`)
	if _, _, err := ParseSnapshot(raw); err == nil {
		t.Fatal("divisão quebrada passou")
	}
}
