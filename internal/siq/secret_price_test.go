package siq

import (
	"siplatform/internal/packs"
	"strings"
	"testing"
)

func TestSecretPriceRangeSemantics(t *testing.T) {
	for _, tc := range []struct {
		value          string
		min, max, step int
	}{
		{"", 0, 0, 0}, {"300", 300, 300, 0}, {"[100;500]", 100, 500, 400}, {"[100;550]/200", 100, 550, 200},
	} {
		t.Run(tc.value, func(t *testing.T) {
			n, err := legacyPrice(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			value := func(p *int) int {
				if p == nil {
					return 0
				}
				return *p
			}
			if value(n.Minimum) != tc.min || value(n.Maximum) != tc.max || value(n.Step) != tc.step {
				t.Fatalf("range changed: %+v", n)
			}
		})
	}
	// A native numeric zero step means any integer; it must survive SIQ validation.
	xml := strings.Replace(v5, `</params>`, `<param name="price" type="numberSet"><numberSet minimum="100" maximum="550" step="0"/></param></params>`, 1)
	a := readArchive(t, archiveBytes(t, entry{"content.xml", []byte(xml), 0}, entry{"Images/кот.png", []byte("image"), 0}))
	if !packs.Playable(a.Issues) {
		t.Fatalf("zero step rejected: %+v", a.Issues)
	}
}
