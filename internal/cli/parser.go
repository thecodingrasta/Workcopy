// Package cli owns argument validation and human/machine presentation. It does
// not interpret shell strings, execute repository scripts, or mutate Git itself.
package cli

import (
	"fmt"
	"strings"
)

type arguments struct {
	command     string
	values      map[string][]string
	positionals []string
}

var valueFlags = map[string]bool{
	"repo": true, "root": true, "workspace": true, "name": true,
	"branch": true, "profile": true, "project": true, "config": true,
	"expect-local": true, "expect-conflict": true, "from-file": true, "review": true,
	"message": true, "side": true, "offset": true, "limit": true,
	"solution": true, "path": true, "target": true, "dependency-identity": true,
	"share": true,
}
var boolFlags = map[string]bool{
	"json": true, "help": true, "dry-run": true, "all": true,
	"force": true, "use-local": true, "use-upstream": true,
	"delete": true, "commit": true, "approve": true,
	"no-commit": true, "trust-shared": true,
}

func parse(input []string) (arguments, error) {
	result := arguments{values: make(map[string][]string)}
	literal := false
	for index := 0; index < len(input); index++ {
		token := input[index]
		if !literal && token == "--" {
			literal = true
			continue
		}
		if !literal && (token == "-h" || token == "--help") {
			result.values["help"] = []string{"true"}
			continue
		}
		if !literal && strings.HasPrefix(token, "--") {
			key, value, equal := strings.Cut(strings.TrimPrefix(token, "--"), "=")
			if key == "work-root" {
				key = "root"
			}
			if boolFlags[key] {
				if equal {
					return result, fmt.Errorf("--%s does not take a value", key)
				}
				if len(result.values[key]) != 0 {
					return result, fmt.Errorf("--%s was specified twice", key)
				}
				result.values[key] = []string{"true"}
				continue
			}
			if !valueFlags[key] {
				return result, fmt.Errorf("unknown option --%s", key)
			}
			if !equal {
				index++
				if index >= len(input) || strings.HasPrefix(input[index], "--") {
					return result, fmt.Errorf("--%s requires a value", key)
				}
				value = input[index]
			}
			if value == "" {
				return result, fmt.Errorf("--%s requires a nonempty value", key)
			}
			if key != "project" && key != "share" && len(result.values[key]) > 0 {
				return result, fmt.Errorf("--%s was specified twice", key)
			}
			result.values[key] = append(result.values[key], value)
			continue
		}
		if !literal && strings.HasPrefix(token, "-") {
			return result, fmt.Errorf("unknown option %s; use -- before a literal argument", token)
		}
		if result.command == "" {
			result.command = token
		} else {
			result.positionals = append(result.positionals, token)
		}
	}
	if result.command == "" {
		result.command = "help"
	}
	return result, nil
}
func (a arguments) value(key string) string {
	if len(a.values[key]) == 0 {
		return ""
	}
	return a.values[key][0]
}
func (a arguments) boolean(key string) bool { return len(a.values[key]) != 0 }

func (a arguments) allowed(additional string) error {
	allowed := " repo root workspace name json help dry-run " + additional + " "
	for key := range a.values {
		if !strings.Contains(allowed, " "+key+" ") {
			return fmt.Errorf("--%s is not valid for %s", key, a.command)
		}
	}
	return nil
}
