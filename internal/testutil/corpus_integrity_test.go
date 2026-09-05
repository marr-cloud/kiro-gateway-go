// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorpusIsReadable recorre todo el corpus generado y comprueba que cada
// caso deserializa a la estructura Case y respeta las invariantes de su kind.
// Si este test falla, ninguna fase posterior puede confiar en el corpus.
func TestCorpusIsReadable(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	base := filepath.Join(root, "testdata")

	var targets []string
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") || d.Name() == "_report.json" {
			return nil
		}
		rel, err := filepath.Rel(base, filepath.Dir(path))
		if err != nil {
			return err
		}
		slug := filepath.ToSlash(rel)
		if len(targets) == 0 || targets[len(targets)-1] != slug {
			targets = append(targets, slug)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("recorriendo el corpus: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("corpus vacío: ejecuta 'task corpus'")
	}
	t.Logf("%d objetivos en el corpus", len(targets))

	total := 0
	for _, target := range targets {
		cases := LoadCorpus(t, target)
		total += len(cases)
		for _, c := range cases {
			if c.Target == "" {
				t.Errorf("%s/%s: target vacío", target, c.Name)
			}
			if c.Commit == "" {
				t.Errorf("%s/%s: recorded_at_commit vacío", target, c.Name)
			}
			switch c.Kind {
			case KindFunction, KindGenerator:
				if len(c.Input) == 0 {
					t.Errorf("%s/%s: kind %q sin input", target, c.Name, c.Kind)
				}
			case KindSequence:
				if len(c.Steps) == 0 {
					t.Errorf("%s/%s: kind sequence sin pasos", target, c.Name)
				}
				for i, s := range c.Steps {
					if s.Method == "" {
						t.Errorf("%s/%s: paso %d sin método", target, c.Name, i)
					}
				}
			default:
				t.Errorf("%s/%s: kind desconocido %q", target, c.Name, c.Kind)
			}
		}
	}
	t.Logf("%d casos válidos en total", total)
}

// TestCorpusReportMatchesFiles comprueba que el informe del grabador cuadra con
// los ficheros realmente presentes, para detectar un corpus commiteado a medias.
// La comprobación es exacta, objetivo a objetivo: si el informe dice 83 casos de
// format_sse_event y en disco hay 40, este test lo dice. Comprobar solo que
// total_cases no es cero dejaba pasar un corpus incompleto.
func TestCorpusReportMatchesFiles(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	base := filepath.Join(root, "testdata")
	raw, err := os.ReadFile(filepath.Join(base, "_report.json"))
	if err != nil {
		t.Fatalf("leyendo _report.json: %v", err)
	}
	var report struct {
		Commit     string         `json:"commit"`
		ExitStatus *int           `json:"upstream_exit_status"`
		Recorded   map[string]int `json:"recorded"`
		TotalCases int            `json:"total_cases"`
		Conflicts  *struct {
			Targets   int `json:"targets"`
			PerTarget map[string]struct {
				WorstKind string `json:"worst_kind"`
			} `json:"per_target"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("deserializando _report.json: %v", err)
	}
	if report.Commit != "a5292ca" {
		t.Errorf("el corpus se grabó del commit %q, quiero a5292ca", report.Commit)
	}
	if report.ExitStatus == nil {
		t.Error("_report.json no trae upstream_exit_status")
	} else if *report.ExitStatus != 0 {
		t.Errorf("la suite del upstream terminó con exitstatus %d al grabar", *report.ExitStatus)
	}
	if len(report.Recorded) == 0 {
		t.Fatal("el informe no declara ningún objetivo")
	}

	// Recuento real de ficheros de caso por objetivo.
	actual := map[string]int{}
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") || d.Name() == "_report.json" {
			return nil
		}
		rel, err := filepath.Rel(base, filepath.Dir(path))
		if err != nil {
			return err
		}
		actual[filepath.ToSlash(rel)]++
		return nil
	})
	if err != nil {
		t.Fatalf("recorriendo el corpus: %v", err)
	}

	total := 0
	for _, n := range actual {
		total += n
	}
	if report.TotalCases != total {
		t.Errorf("el informe dice %d casos y en disco hay %d", report.TotalCases, total)
	}
	for target, said := range report.Recorded {
		if real := actual[target]; real != said {
			t.Errorf("%s: el informe dice %d casos y en disco hay %d", target, said, real)
		}
	}
	for target, real := range actual {
		if _, ok := report.Recorded[target]; !ok {
			t.Errorf("%s: %d casos en disco que el informe no declara", target, real)
		}
	}

	// Los conflictos de conducta invalidan la correspondencia entrada -> salida en
	// la que se apoya cualquier test golden. validate.py ya los rechaza; esto lo
	// repite del lado de Go para que `go test ./...` a secas también lo vea.
	if report.Conflicts == nil {
		t.Fatal("_report.json no trae la sección conflicts: regenera el corpus con 'task corpus'")
	}
	for target, info := range report.Conflicts.PerTarget {
		if info.WorstKind != "identifier_only" {
			t.Errorf("%s: conflictos de clase %q en el corpus; ver docs/CORPUS.md",
				target, info.WorstKind)
		}
	}
}
