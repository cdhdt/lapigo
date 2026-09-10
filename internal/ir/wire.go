package ir

// ReservedQueryParams are the query-parameter names spec §6.9.4 reserves for
// pagination: "limit" and "after". A filter whose wire name -- Field.Column,
// per §6.9.2's "one vocabulary, used everywhere a field is named on the
// wire" -- resolves to one of these would be shadowed by pagination, so
// internal/validate rejects it (spec §5.6: "reject, never mangle").
//
// This lives in internal/ir, not internal/validate, on purpose. §6.9.4 is
// explicit that the reserved set grows once step 8 lands the rest of the
// query contract, and internal/gen's future httpapi templates need the same
// vocabulary internal/validate checks against -- both packages already
// import ir, so a list stored here is read, not retyped, by whichever of
// them needs it. internal/validate's own reservedMethodNames (names.go)
// could not do the same: it was written ahead of internal/gen, which does
// not exist yet, and its doc comment records the debt in so many words --
// "This list MUST be kept in sync with internal/gen's templates once they
// exist" -- because nothing but that comment links the two. Putting this
// list in ir instead of repeating that pattern means growing it for step 8
// updates one place both sides already read.
var ReservedQueryParams = []string{"limit", "after"}

// reservedQueryParamSet is ReservedQueryParams as a set, built once from the
// literal slice above so a membership test never ranges a map (the same
// determinism concern internal/validate's reservedMethodNameSet documents).
var reservedQueryParamSet = func() map[string]bool {
	m := make(map[string]bool, len(ReservedQueryParams))
	for _, name := range ReservedQueryParams {
		m[name] = true
	}
	return m
}()

// IsReservedQueryParam reports whether name is one of ReservedQueryParams.
func IsReservedQueryParam(name string) bool {
	return reservedQueryParamSet[name]
}
