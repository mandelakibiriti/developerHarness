package budget

import "testing"

func TestAlertsFireOnceAndInOrder(t *testing.T) {
	tr := New(10.0, []float64{0.8, 1.0})

	if got := tr.Add(5.0); len(got) != 0 {
		t.Fatalf("no threshold at 5/10, got %v", got)
	}
	if got := tr.Add(3.0); len(got) != 1 || got[0] != 0.8 {
		t.Fatalf("80%% threshold at 8/10, got %v", got)
	}
	if got := tr.Add(2.0); len(got) != 1 || got[0] != 1.0 {
		t.Fatalf("100%% threshold at 10/10, got %v", got)
	}
	if got := tr.Add(5.0); len(got) != 0 {
		t.Fatalf("thresholds must not re-fire, got %v", got)
	}
	if !tr.Over() {
		t.Fatal("expected Over() true")
	}
	st := tr.Status()
	if st.Spent != 15.0 || st.Ratio != 1.5 {
		t.Fatalf("status: %+v", st)
	}
}

func TestZeroBudget(t *testing.T) {
	tr := New(0, []float64{0.8, 1.0})
	if got := tr.Add(100); len(got) != 0 {
		t.Fatalf("no budget means no alerts, got %v", got)
	}
	if st := tr.Status(); st.Ratio != 0 {
		t.Fatalf("ratio should be 0, got %f", st.Ratio)
	}
}
