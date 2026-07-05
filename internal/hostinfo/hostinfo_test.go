package hostinfo

import "testing"

func TestParseCPUStat(t *testing.T) {
	idle, total, ok := parseCPUStat("cpu  100 0 50 800 50 0 0 0 0 0")
	if !ok || idle != 850 || total != 1000 {
		t.Errorf("parseCPUStat = %d, %d, %v; want 850, 1000, true", idle, total, ok)
	}
	for _, bad := range []string{"", "cpu0 1 2 3 4 5", "cpu one two three four"} {
		if _, _, ok := parseCPUStat(bad); ok {
			t.Errorf("parseCPUStat(%q): want !ok", bad)
		}
	}
}
