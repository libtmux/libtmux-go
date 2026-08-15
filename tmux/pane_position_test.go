package tmux

import "testing"

// libtmux:parity libtmux.pane.Pane.at_bottom
// libtmux:parity libtmux.pane.Pane.at_left
// libtmux:parity libtmux.pane.Pane.at_right
// libtmux:parity libtmux.pane.Pane.at_top
func TestPanePositionAccessorsConvertPresentFlagsAndPreserveAbsence(t *testing.T) {
	t.Parallel()

	pane := Pane{formats: formatValues{values: map[string]string{
		"pane_at_top":    "1",
		"pane_at_bottom": "0",
		"pane_at_left":   "1",
		"pane_at_right":  "0",
	}}}
	for _, test := range []struct {
		name string
		get  func() (bool, bool)
		want bool
	}{
		{name: "top", get: pane.AtTop, want: true},
		{name: "bottom", get: pane.AtBottom, want: false},
		{name: "left", get: pane.AtLeft, want: true},
		{name: "right", get: pane.AtRight, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, ok := test.get()
			if !ok || value != test.want {
				t.Fatalf("position = (%t, %t), want (%t, true)", value, ok, test.want)
			}
		})
	}

	missing := Pane{}
	for name, get := range map[string]func() (bool, bool){
		"top": missing.AtTop, "bottom": missing.AtBottom,
		"left": missing.AtLeft, "right": missing.AtRight,
	} {
		if value, ok := get(); ok || value {
			t.Fatalf("missing %s = (%t, %t), want (false, false)", name, value, ok)
		}
	}
}
