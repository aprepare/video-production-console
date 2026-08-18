package montageplan

import "strings"

// interleaveByCategory reorders an already ranked pool so that neighbouring
// clips prefer different categories. The result stays fully deterministic:
// group order follows each category's first appearance in the input, members
// keep their incoming relative order, and no map iteration or randomness is
// involved. When a single category remains it is emitted in ranked order,
// so a mono-category library degrades to the incoming sequence.
func interleaveByCategory(items []mediaItem) []mediaItem {
	if len(items) < 2 {
		return items
	}
	type categoryGroup struct {
		category string
		members  []mediaItem
		cursor   int
	}
	groups := make([]*categoryGroup, 0, 8)
	byCategory := make(map[string]*categoryGroup, 8)
	for _, item := range items {
		key := normalizeCategory(item.Category)
		group, ok := byCategory[key]
		if !ok {
			group = &categoryGroup{category: key}
			byCategory[key] = group
			groups = append(groups, group)
		}
		group.members = append(group.members, item)
	}
	if len(groups) < 2 {
		return items
	}

	remaining := func(group *categoryGroup) int { return len(group.members) - group.cursor }
	out := make([]mediaItem, 0, len(items))
	last := ""
	hasLast := false
	for len(out) < len(items) {
		picked := -1
		for i, group := range groups {
			if remaining(group) == 0 {
				continue
			}
			if hasLast && group.category == last {
				continue
			}
			// Draining the largest group first postpones the point where only
			// one category is left; ties keep the earlier first appearance.
			if picked == -1 || remaining(group) > remaining(groups[picked]) {
				picked = i
			}
		}
		if picked == -1 {
			for _, group := range groups {
				for ; group.cursor < len(group.members); group.cursor++ {
					out = append(out, group.members[group.cursor])
				}
			}
			break
		}
		group := groups[picked]
		out = append(out, group.members[group.cursor])
		group.cursor++
		last = group.category
		hasLast = true
	}
	return out
}

func normalizeCategory(category string) string {
	return strings.ToLower(strings.TrimSpace(category))
}
