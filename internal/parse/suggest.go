package parse

// suggestThreshold is the maximum Levenshtein distance suggest treats as
// "close enough to name in a hint". Beyond this, guessing intent does more
// harm than good -- an unrelated key suggested as a typo fix confuses more
// than it helps (spec §4.1's "actionable fix" bar cuts both ways).
const suggestThreshold = 2

// suggest returns the entry in known closest to got by Levenshtein distance,
// or the empty string if the closest entry is farther than suggestThreshold.
// Used to turn an unknown schema key into a hint naming the likely intent
// ("did you mean `type`?") rather than a bare rejection.
func suggest(got string, known []string) string {
	best := ""
	bestDist := suggestThreshold + 1
	for _, k := range known {
		d := levenshtein(got, k)
		if d < bestDist {
			bestDist = d
			best = k
		}
	}
	if bestDist > suggestThreshold {
		return ""
	}
	return best
}

// levenshtein returns the edit distance between a and b: the minimum number
// of single-rune insertions, deletions, or substitutions to turn one into
// the other. Classic dynamic-programming table, O(len(a)*len(b)) time and
// O(len(b)) space -- schema key names are a handful of runes, so neither
// bound matters in practice.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
