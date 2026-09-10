package gen

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/tools/imports"
)

// formatHint is the second line of every formatting failure. It is a
// constant because the message is a contract the tests assert whole, and
// because it says something a reader needs: rendered text that does not
// parse is usually a template bug, but not always -- user data reaches Go
// source through comments and string literals, which is why the offending
// output is printed underneath it (spec §5.2).
const formatHint = "usually a template bug; check the schema values quoted below"

// formatOptions are the options every generated file is formatted with.
//
// FormatOnly is the load-bearing one (spec §5.2, §13's 2026-09-10 row).
// With resolution enabled, goimports scans the machine's module cache and
// shells out to `go list`: measured against a go.mod correctly requiring
// github.com/jackc/pgx/v5, it added the pre-v5 github.com/jackc/pgx that
// happened to be in that cache and exited 0, and against an empty cache it
// dropped the pgx import entirely and exited 0. Same schema, same go.mod,
// different bytes per developer -- a determinism violation no sorting fixes,
// and a silent one in both directions.
//
// FormatOnly still runs gofmt and still sorts each import block, so the
// formatting contract is unchanged; only the resolution is gone. The import
// set is the plan step's (plan.go), and the correction pass for a wrong set
// is the Go compiler, which reports both a missing and a surplus import --
// strictly more than goimports offers, since goimports reports neither.
var formatOptions = &imports.Options{
	Comments:   true,
	TabIndent:  true,
	TabWidth:   8,
	FormatOnly: true,
}

// format runs src through gofmt-with-import-sorting and returns the result.
//
// path is passed only as the name the parser puts in front of a syntax
// error's line:column. With FormatOnly set, imports.Process never looks at
// the filesystem -- resolution is the only thing that would, and it is
// switched off -- so a path naming a directory that does not exist yet, or
// one that is about to be renamed away by the staging swap (spec §5.4), is
// harmless here. A hermetic formatter has no filename to be wrong about.
//
// On failure the UNFORMATTED text is presented with line numbers, because
// the line:column the parser reports is a position in that text and in no
// other (spec §5.2). Generation then fails hard: no partial and no
// unformatted Go is ever returned.
func format(path string, src []byte) ([]byte, error) {
	out, err := imports.Process(path, src, formatOptions)
	if err != nil {
		// The parser's own error already carries path:line:column, because
		// path is the name it was given, so naming the file again here
		// would print it twice.
		return nil, fmt.Errorf("gen: rendered output is not valid Go: %w\n%s\n%s",
			err, formatHint, numberLines(src))
	}
	return out, nil
}

// numberLines renders src with a right-aligned line number and a "|" gutter
// in front of every line, so that a reported line:column can be found by
// eye. The gutter width is the width of the largest line number, so the
// source stays aligned in a file of any length.
//
// A trailing newline does not produce a numbered empty last line: the file
// has as many lines as it has, and inventing one shifts nothing but does
// invite a reader to look for content that is not there.
func numberLines(src []byte) string {
	lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	width := len(strconv.Itoa(len(lines)))

	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%*d | %s\n", width, i+1, line)
	}
	return b.String()
}
