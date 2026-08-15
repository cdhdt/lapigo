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
//
// bestDist tracks the true minimum distance across the whole of known,
// unbounded by suggestThreshold -- only the final comparison applies the
// threshold. An earlier version started bestDist at suggestThreshold+1 and
// only ever lowered it, which meant every update already left bestDist
// <= suggestThreshold: the final "bestDist > suggestThreshold" guard could
// only ever be true when bestDist was untouched, a case already implied by
// best == "". That guard was therefore dead code -- deleting it changed
// nothing, which a mutation-testing pass caught. Tracking the unbounded
// minimum here makes the guard load-bearing: bestDist can genuinely exceed
// suggestThreshold when the nearest known entry is still far away.
//
// Ties are broken deterministically by the lexicographically smaller
// candidate (d == bestDist && k < best), not by insertion order -- callers
// must pass a vocabulary already in a stable order (sorted, not ranged from
// a map) for this to be reproducible across processes; see
// endpointKeywordNames and typeKeywordSuggestions.
func suggest(got string, known []string) string {
	best := ""
	haveBest := false
	bestDist := 0
	for _, k := range known {
		d := levenshtein(got, k)
		if !haveBest || d < bestDist || (d == bestDist && k < best) {
			bestDist = d
			best = k
			haveBest = true
		}
	}
	if !haveBest || bestDist > suggestThreshold {
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
