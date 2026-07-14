package catalog

import (
	"fmt"
	"sort"
)

var aliasEntityKinds = []EntityKind{
	EntityKindCase,
	EntityKindPrinciple,
	EntityKindLab,
	EntityKindSource,
}

type aliasKey struct {
	kind EntityKind
	from string
}

type AliasSet struct {
	terminals map[aliasKey]string
}

func NewAliasSet(aliases []Alias) (AliasSet, error) {
	direct := make(map[aliasKey]string, len(aliases))
	keys := make([]aliasKey, 0, len(aliases))
	for _, alias := range aliases {
		if !isAliasEntityKind(alias.Kind) {
			return AliasSet{}, fmt.Errorf("unsupported alias kind %q", alias.Kind)
		}
		if alias.From == "" {
			return AliasSet{}, fmt.Errorf("%s alias has empty alias from", alias.Kind)
		}
		if alias.To == "" {
			return AliasSet{}, fmt.Errorf("%s alias %q has empty alias to", alias.Kind, alias.From)
		}
		if alias.From == alias.To {
			return AliasSet{}, fmt.Errorf("%s alias %q is a self-alias", alias.Kind, alias.From)
		}

		key := aliasKey{kind: alias.Kind, from: alias.From}
		if _, exists := direct[key]; exists {
			return AliasSet{}, fmt.Errorf("duplicate alias for %s %q", alias.Kind, alias.From)
		}
		direct[key] = alias.To
		keys = append(keys, key)
	}

	sortAliasKeys(keys)
	set := AliasSet{terminals: make(map[aliasKey]string, len(aliases))}
	state := make(map[aliasKey]uint8, len(aliases))
	var cacheTerminal func(aliasKey) (string, error)
	cacheTerminal = func(key aliasKey) (string, error) {
		switch state[key] {
		case 1:
			return "", fmt.Errorf("alias cycle for %s involving %q", key.kind, key.from)
		case 2:
			return set.terminals[key], nil
		}

		state[key] = 1
		terminal := direct[key]
		next := aliasKey{kind: key.kind, from: terminal}
		if _, exists := direct[next]; exists {
			var err error
			terminal, err = cacheTerminal(next)
			if err != nil {
				return "", err
			}
		}
		state[key] = 2
		set.terminals[key] = terminal
		return terminal, nil
	}

	for _, key := range keys {
		if _, err := cacheTerminal(key); err != nil {
			return AliasSet{}, err
		}
	}
	return set, nil
}

func (a AliasSet) ValidateCanonical(canonical map[EntityKind]map[string]struct{}) error {
	keys := make([]aliasKey, 0, len(a.terminals))
	for key := range a.terminals {
		keys = append(keys, key)
	}
	sortAliasKeys(keys)

	for _, key := range keys {
		if _, exists := canonical[key.kind][key.from]; exists {
			return fmt.Errorf("%s alias %q shadows canonical ID", key.kind, key.from)
		}
		terminal := a.terminals[key]
		if _, exists := canonical[key.kind][terminal]; exists {
			continue
		}
		for _, otherKind := range aliasEntityKinds {
			if otherKind == key.kind {
				continue
			}
			if _, exists := canonical[otherKind][terminal]; exists {
				return fmt.Errorf("%s alias %q terminal %q exists as %s", key.kind, key.from, terminal, otherKind)
			}
		}
		return fmt.Errorf("%s alias %q has missing terminal %q", key.kind, key.from, terminal)
	}
	return nil
}

func (a AliasSet) Resolve(kind EntityKind, id string) (string, error) {
	if !isAliasEntityKind(kind) {
		return "", fmt.Errorf("unsupported alias kind %q", kind)
	}
	if terminal, exists := a.terminals[aliasKey{kind: kind, from: id}]; exists {
		return terminal, nil
	}
	return id, nil
}

func isAliasEntityKind(kind EntityKind) bool {
	for _, allowed := range aliasEntityKinds {
		if kind == allowed {
			return true
		}
	}
	return false
}

func sortAliasKeys(keys []aliasKey) {
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].kind != keys[right].kind {
			return keys[left].kind < keys[right].kind
		}
		return keys[left].from < keys[right].from
	})
}
