package examples_test

import (
	"siplatform/examples"
	"siplatform/internal/playpack"
	"testing"
)

func TestDemoShowsEveryPlayableMechanicAndFinal(t *testing.T) {
	d := examples.Demo()
	seen := map[string]bool{}
	final := false
	for _, r := range d.Rounds {
		spec, err := playpack.Round(d, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Type == "final" {
			final = true
			if len(r.Themes) < 3 {
				t.Fatal("final needs theme elimination")
			}
		}
		for _, theme := range r.Themes {
			for _, q := range theme.Questions {
				compiled, err := playpack.Compile(d, q.ID)
				if err != nil {
					t.Fatal(err)
				}
				seen[compiled.Type] = true
			}
		}
	}
	for _, kind := range []string{"withButton", "stake", "secret", "forAll", "noRisk", "stakeAll"} {
		if !seen[kind] {
			t.Errorf("missing demonstration: %s", kind)
		}
	}
	if !final {
		t.Error("missing final")
	}
}
