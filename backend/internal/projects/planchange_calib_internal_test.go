package projects

import "testing"

func TestSimilarTitleCatchesRewordedRepeatsButKeepsOtherSubsidiaries(t *testing.T) {
	same := [][2]string{
		{"Pedir a la filial Chile el paquete de cierre del periodo", "Solicitar a la filial Chile el paquete de cierre del período (balance, mayor)"},
		{"Confirmar con el CFO periodo, moneda funcional", "confirmar con el cfo: periodo y moneda funcional"},
	}
	for _, p := range same {
		if !similarTitle(titleWords(p[0]), titleWords(p[1])) {
			t.Errorf("should be a repeat: %q / %q", p[0], p[1])
		}
	}
	other := [][2]string{
		{"Conciliar intercompañías de México", "Conciliar intercompañías de Chile"},
		{"Calcular la provisión de impuestos de Colombia", "Revisar el contrato de arrendamiento"},
	}
	for _, p := range other {
		if similarTitle(titleWords(p[0]), titleWords(p[1])) {
			t.Errorf("should be different work: %q / %q", p[0], p[1])
		}
	}
}

func TestCostCalibrationUsesTheRealSpendOfFinishedWork(t *testing.T) {
	nodes := []NodeDef{{ID: "a", Kind: KindTask, EstCostUSD: 0.01}, {ID: "b", Kind: KindTask, EstCostUSD: 0.01},
		{ID: "c", Kind: KindTask, EstCostUSD: 0.01}, {ID: "d", Kind: KindTask, EstCostUSD: 0.01}}
	done := map[string]string{"a": StateDone, "b": StateDone, "c": StateDone}
	if got := costCalibration(nodes, done, 0.30); got < 9.99 || got > 10.01 {
		t.Fatalf("calibration = %v, want 10", got)
	}
	if got := costCalibration(nodes, done, 0.01); got != 1 {
		t.Fatalf("cheaper than the priors never lowers the estimate, got %v", got)
	}
	if got := costCalibration(nodes, map[string]string{"a": StateDone}, 5); got != 1 {
		t.Fatalf("too little evidence, got %v", got)
	}
	if got := costCalibration(nodes, done, 1e6); got != maxCalibration {
		t.Fatalf("capped, got %v", got)
	}
}
