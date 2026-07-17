// plancheck is the T6 QA deterministic plan-integrity gate (work package Q1):
// given the approved plan markdown it verifies that task IDs are unique, every
// depends-on target exists, the dependency graph is acyclic, and every prose
// task-number reference (T<n>) resolves to a declared lane. It prints one
// PASS/FAIL line per rule and exits non-zero on any violation, so the check is
// binary and reproducible:
//
//	go run ./scripts/qa/plancheck .guild/plan/amux-go-linux-runtime.md
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var (
	taskIDLine  = regexp.MustCompile(`(?m)^-\s*task-id:\s*(\S+)`)
	ownerLine   = regexp.MustCompile(`(?m)^-\s*owner:\s*(\S+)`)
	dependsLine = regexp.MustCompile(`(?m)^-\s*depends-on:\s*\[([^\]]*)\]`)
	proseRef    = regexp.MustCompile(`\bT(\d+)\b`)
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: plancheck <plan.md>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	text := string(raw)
	fail := 0
	check := func(name string, ok bool, detail string) {
		verdict := "PASS"
		if !ok {
			verdict = "FAIL"
			fail++
		}
		fmt.Printf("%s %s%s\n", verdict, name, detail)
	}

	// Lane declarations, in document order.
	ids := taskIDLine.FindAllStringSubmatch(text, -1)
	owners := ownerLine.FindAllStringSubmatch(text, -1)
	depsRaw := dependsLine.FindAllStringSubmatch(text, -1)

	declared := map[string]bool{}
	dup := []string{}
	var order []string
	for _, m := range ids {
		if declared[m[1]] {
			dup = append(dup, m[1])
		}
		declared[m[1]] = true
		order = append(order, m[1])
	}
	check("task-ids-declared", len(order) > 0, fmt.Sprintf(" (%d lanes: %s)", len(order), strings.Join(order, ", ")))
	check("task-ids-unique", len(dup) == 0, detailList(dup))
	check("owner-per-lane", len(owners) == len(order), fmt.Sprintf(" (%d owners for %d lanes)", len(owners), len(order)))

	// Dependency existence + acyclicity (Kahn).
	deps := map[string][]string{}
	missing := []string{}
	for i, m := range depsRaw {
		if i >= len(order) {
			break
		}
		lane := order[i]
		for _, d := range strings.Split(m[1], ",") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			deps[lane] = append(deps[lane], d)
			if !declared[d] {
				missing = append(missing, lane+"->"+d)
			}
		}
	}
	check("depends-on-exist", len(missing) == 0, detailList(missing))

	resolved := map[string]bool{}
	progress := true
	for progress {
		progress = false
		for _, lane := range order {
			if resolved[lane] {
				continue
			}
			ok := true
			for _, d := range deps[lane] {
				if declared[d] && !resolved[d] {
					ok = false
					break
				}
			}
			if ok {
				resolved[lane] = true
				progress = true
			}
		}
	}
	cyc := []string{}
	for _, lane := range order {
		if !resolved[lane] {
			cyc = append(cyc, lane)
		}
	}
	check("dag-acyclic", len(cyc) == 0, detailList(cyc))

	// Every prose T<n> reference resolves to a declared lane prefix.
	nums := map[string]bool{}
	for id := range declared {
		m := proseRef.FindStringSubmatch(id)
		if m != nil {
			nums["T"+m[1]] = true
		}
	}
	unknown := map[string]bool{}
	for _, m := range proseRef.FindAllStringSubmatch(text, -1) {
		if !nums["T"+m[1]] {
			unknown["T"+m[1]] = true
		}
	}
	var unknownList []string
	for k := range unknown {
		unknownList = append(unknownList, k)
	}
	sort.Strings(unknownList)
	check("prose-refs-resolve", len(unknownList) == 0, detailList(unknownList))

	if fail > 0 {
		fmt.Printf("plancheck: %d violation(s)\n", fail)
		os.Exit(1)
	}
	fmt.Println("plancheck: all rules hold")
}

func detailList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return " (" + strings.Join(items, ", ") + ")"
}
