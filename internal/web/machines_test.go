package web

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderMachinesEscapesValues(t *testing.T) {
	var out bytes.Buffer
	err := RenderMachinesPage(&out, "csrf", []Machine{{ID: "machine-1", Name: "<mesa>", Freshness: "Vista", Boot: "boot-1", PaneCount: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/maquinas/machine-1", "&lt;mesa&gt;", "Vista", "boot-1", "2 panes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("valor ausente: %s", want)
		}
	}
}

func TestChartAnchorsOnLatestReceivedObservation(t *testing.T) {
	anchor := time.Date(2026, 11, 13, 1, 0, 35, 0, time.UTC)
	chart := Chart(anchor, []time.Time{anchor, anchor.Add(-2 * time.Minute), anchor.Add(-20 * time.Minute)})
	if len(chart.Slots) != 15 || chart.Slots[14].Class != "beat" || chart.Slots[13].Class != "miss" || chart.Slots[12].Class != "beat" || !strings.Contains(chart.Label, "01:00 UTC: 2 de 15") {
		t.Fatalf("faixa incorreta: %+v", chart)
	}
}

func TestRenderMachinesEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := RenderMachinesPage(&out, "csrf", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nenhuma máquina. Gere um código e instale o agente.") {
		t.Fatal("mensagem vazia ausente")
	}
}
