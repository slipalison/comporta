// Package guard classifies SQL statements into the operations they perform so
// the gateway can enforce per-connection permissions before anything reaches
// the database.
//
// The classifier is deliberately conservative: anything it cannot positively
// identify as a read is treated as DDL/admin. It is the first of three layers;
// reads additionally run inside a read-only (or rolled back) transaction, and
// the database user itself should only have the grants it needs.
package guard

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/slipalison/comporta/internal/model"
)

type Analysis struct {
	// Ops lists the permissions the statement needs besides select. Empty
	// means the statement is a plain read.
	Ops []model.Op
	// Leading is the first keyword of the statement (upper case).
	Leading string
	// ReturnsRows is true for reads and for writes with RETURNING / OUTPUT.
	ReturnsRows bool
}

func (a Analysis) ReadOnly() bool { return len(a.Ops) == 0 }

// Operation is a short label for the audit log.
func (a Analysis) Operation() string {
	if a.ReadOnly() {
		return string(model.OpSelect)
	}
	parts := make([]string, len(a.Ops))
	for i, op := range a.Ops {
		parts[i] = string(op)
	}
	return strings.Join(parts, "+")
}

type tokKind int

const (
	tkWord tokKind = iota
	tkSemicolon
	tkLParen
	tkDot
	tkOther
)

type token struct {
	kind tokKind
	text string // upper-cased for words
}

var readLeaders = []string{"SELECT", "WITH", "SHOW", "EXPLAIN", "DESCRIBE", "DESC", "VALUES", "TABLE", "("}

// Keywords that need DDL/admin permission wherever they appear.
var adminAnywhere = []string{
	"CREATE", "ALTER", "DROP", "GRANT", "REVOKE", "DENY",
	"EXEC", "EXECUTE", "CALL",
	"OPENQUERY", "OPENROWSET", "OPENDATASOURCE",
}

// T-SQL lets statements follow each other without a separator, so for SQL
// Server these are checked anywhere in the text, not only at the start.
var mssqlAdminAnywhere = []string{
	"BEGIN", "COMMIT", "ROLLBACK", "SAVE", "USE", "DBCC", "KILL", "SHUTDOWN",
	"BACKUP", "RESTORE", "RECONFIGURE", "BULK", "WAITFOR", "SETUSER", "REVERT",
	"CHECKPOINT", "DECLARE", "GOTO",
}

// AnalyzeSQL tokenizes the query for the given dialect and returns the
// operations it needs. Exactly one statement is accepted.
func AnalyzeSQL(dbType model.DBType, query string) (Analysis, error) {
	toks, err := tokenize(dbType, query)
	if err != nil {
		return Analysis{}, err
	}
	var stmts [][]token
	var cur []token
	for _, t := range toks {
		if t.kind == tkSemicolon {
			if len(cur) > 0 {
				stmts = append(stmts, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		stmts = append(stmts, cur)
	}
	switch len(stmts) {
	case 0:
		return Analysis{}, errors.New("empty SQL statement")
	case 1:
	default:
		return Analysis{}, fmt.Errorf("only one SQL statement per call is allowed (found %d separated by ';')", len(stmts))
	}
	if err := forbidden(dbType, stmts[0]); err != nil {
		return Analysis{}, err
	}
	return analyze(dbType, stmts[0]), nil
}

// forbidden rejects statements no permission can allow. In SQLite, ATTACH
// opens any file the gateway can read as a second schema, including the
// gateway's own database, where a connection could rewrite its permissions.
func forbidden(dbType model.DBType, toks []token) error {
	if dbType != model.SQLite {
		return nil
	}
	for i, t := range toks {
		if t.kind == tkWord && (t.text == "ATTACH" || t.text == "DETACH") && (i == 0 || toks[i-1].kind != tkDot) {
			return fmt.Errorf("%s is not allowed: a connection only reaches its own database file", t.text)
		}
	}
	return nil
}

func analyze(dbType model.DBType, toks []token) Analysis {
	leading := leadingKeyword(toks)
	ops, seen := keywordOps(dbType, toks)
	statementOps(dbType, leading, seen, ops)

	var list []model.Op
	for _, op := range []model.Op{model.OpInsert, model.OpUpdate, model.OpDelete, model.OpDDL} {
		if ops[op] {
			list = append(list, op)
		}
	}
	return Analysis{
		Ops:         list,
		Leading:     leading,
		ReturnsRows: len(list) == 0 || seen["RETURNING"] || (dbType == model.SQLServer && seen["OUTPUT"]),
	}
}

func leadingKeyword(toks []token) string {
	switch toks[0].kind {
	case tkLParen:
		return "("
	case tkWord:
		return toks[0].text
	}
	return ""
}

// keywordOps marks the operations that keywords require wherever they appear,
// and returns every keyword seen.
func keywordOps(dbType model.DBType, toks []token) (map[model.Op]bool, map[string]bool) {
	ops := map[model.Op]bool{}
	seen := map[string]bool{}
	for i, t := range toks {
		// a.update / schema.delete are identifiers, not keywords
		if t.kind != tkWord || (i > 0 && toks[i-1].kind == tkDot) {
			continue
		}
		seen[t.text] = true
		// INSERT(...), REPLACE(...), TRUNCATE(...) are MySQL functions
		nextParen := i+1 < len(toks) && toks[i+1].kind == tkLParen
		for _, op := range keywordWrites(t.text, nextParen) {
			ops[op] = true
		}
		if slices.Contains(adminAnywhere, t.text) || (dbType == model.SQLServer && slices.Contains(mssqlAdminAnywhere, t.text)) {
			ops[model.OpDDL] = true
		}
	}
	return ops, seen
}

// keywordWrites is what a single write keyword requires.
func keywordWrites(kw string, nextParen bool) []model.Op {
	switch {
	case kw == "INSERT" && !nextParen:
		return []model.Op{model.OpInsert}
	case kw == "UPDATE" && !nextParen:
		return []model.Op{model.OpUpdate}
	case kw == "DELETE":
		return []model.Op{model.OpDelete}
	case kw == "MERGE":
		return []model.Op{model.OpInsert, model.OpUpdate, model.OpDelete}
	case kw == "TRUNCATE" && !nextParen:
		return []model.Op{model.OpDelete, model.OpDDL}
	}
	return nil
}

// statementOps adds what the statement as a whole requires: SELECT INTO,
// T-SQL SET, and the leading keyword.
func statementOps(dbType model.DBType, leading string, seen map[string]bool, ops map[model.Op]bool) {
	// SELECT ... INTO creates a table (or a file / variable in MySQL).
	if seen["INTO"] && !seen["INSERT"] && !seen["MERGE"] && leading != "REPLACE" {
		ops[model.OpInsert], ops[model.OpDDL] = true, true
	}
	// T-SQL: a SET outside UPDATE ... SET changes session state.
	if dbType == model.SQLServer && seen["SET"] && !seen["UPDATE"] && !seen["MERGE"] {
		ops[model.OpDDL] = true
	}
	switch {
	case slices.Contains(readLeaders, leading):
	case leading == "INSERT", leading == "UPDATE", leading == "DELETE", leading == "MERGE":
	case leading == "REPLACE": // REPLACE INTO deletes the conflicting row, then inserts
		ops[model.OpInsert], ops[model.OpDelete] = true, true
	case leading == "UPSERT":
		ops[model.OpInsert], ops[model.OpUpdate] = true, true
	default: // SET, BEGIN, COPY, LOAD, DO, VACUUM, PRAGMA, ATTACH, LOCK, ... or unknown
		ops[model.OpDDL] = true
	}
}

func isWordStart(dbType model.DBType, c byte) bool {
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c >= 0x80 {
		return true
	}
	// @var, @@ROWCOUNT, #temp are identifiers in T-SQL
	return dbType == model.SQLServer && (c == '@' || c == '#')
}

func isWordChar(dbType model.DBType, c byte) bool {
	return isWordStart(dbType, c) || c >= '0' && c <= '9' || c == '$'
}

// tokenize strips comments, string literals and quoted identifiers following
// the rules of each dialect. Getting these rules exactly right matters: if the
// tokenizer thought a region was a literal while the database executes it as
// code, a write could slip past the classifier.
func tokenize(dbType model.DBType, s string) ([]token, error) {
	l := lexer{db: dbType, s: s}
	var toks []token
	for i := 0; i < len(s); {
		next, tok, err := l.next(i)
		if err != nil {
			return nil, err
		}
		if tok != nil {
			toks = append(toks, *tok)
		}
		i = next
	}
	return toks, nil
}

// lexer reads one token (or one skipped region) at a time.
type lexer struct {
	db model.DBType
	s  string
}

var other = &token{kind: tkOther}

// next reads what starts at i and returns where the following one starts,
// with the token read, or nil for whitespace and comments.
func (l lexer) next(i int) (int, *token, error) {
	c := l.s[i]
	switch {
	case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
		return i + 1, nil, nil
	case l.at(i, "--") || (c == '#' && l.db == model.MySQL):
		return l.skipLine(i), nil, nil
	case l.at(i, "/*"):
		end, err := l.skipBlock(i)
		return end, nil, err
	}
	if closing, ok := l.quote(c); ok {
		// Only MySQL escapes with a backslash, and only inside '...' and "...".
		end, err := skipQuoted(l.s, i, closing, l.db == model.MySQL && (c == '\'' || c == '"'))
		return end, other, err
	}
	switch {
	case c == '$' && l.db == model.Postgres:
		return l.dollar(i)
	case isWordStart(l.db, c):
		j := l.span(i+1, func(b byte) bool { return isWordChar(l.db, b) })
		return j, &token{kind: tkWord, text: strings.ToUpper(l.s[i:j])}, nil
	case c >= '0' && c <= '9':
		return l.span(i+1, func(b byte) bool { return isWordChar(l.db, b) || b == '.' }), other, nil
	}
	return i + 1, punctuation(c), nil
}

func (l lexer) at(i int, prefix string) bool { return strings.HasPrefix(l.s[i:], prefix) }

// span returns the first index from i whose byte does not satisfy ok.
func (l lexer) span(i int, ok func(byte) bool) int {
	for i < len(l.s) && ok(l.s[i]) {
		i++
	}
	return i
}

func (l lexer) skipLine(i int) int {
	return l.span(i, func(b byte) bool { return b != '\n' })
}

// skipBlock skips a /* comment */. Postgres and SQL Server nest them; MySQL
// executable comments (/*! ... */) run as code and are refused.
func (l lexer) skipBlock(i int) (int, error) {
	if l.db == model.MySQL && (l.at(i, "/*!") || l.at(i, "/*M!")) {
		return 0, errors.New("MySQL executable comments (/*! ... */) are not allowed")
	}
	nested := l.db == model.Postgres || l.db == model.SQLServer
	depth := 1
	for i += 2; i < len(l.s) && depth > 0; {
		switch {
		case l.at(i, "*/"):
			depth--
			i += 2
		case nested && l.at(i, "/*"):
			depth++
			i += 2
		default:
			i++
		}
	}
	if depth > 0 {
		return 0, errors.New("unterminated /* comment")
	}
	return i, nil
}

// quote returns the closing character when c opens a string or a quoted
// identifier in this dialect.
func (l lexer) quote(c byte) (byte, bool) {
	switch {
	case c == '\'' || c == '"':
		return c, true
	case c == '`' && (l.db == model.MySQL || l.db == model.SQLite):
		return '`', true
	case c == '[' && (l.db == model.SQLServer || l.db == model.SQLite):
		return ']', true
	}
	return 0, false
}

// dollar reads $1 (a parameter) or $tag$...$tag$ (a dollar-quoted string).
func (l lexer) dollar(i int) (int, *token, error) {
	j := l.span(i+1, func(b byte) bool { return isWordChar(l.db, b) && b != '$' })
	isParam := j > i+1 && l.s[i+1] >= '0' && l.s[i+1] <= '9'
	if j >= len(l.s) || l.s[j] != '$' || isParam {
		return j, other, nil
	}
	tag := l.s[i : j+1]
	end := strings.Index(l.s[j+1:], tag)
	if end < 0 {
		return 0, nil, errors.New("unterminated dollar-quoted string")
	}
	return j + 1 + end + len(tag), other, nil
}

func punctuation(c byte) *token {
	switch c {
	case ';':
		return &token{kind: tkSemicolon}
	case '(':
		return &token{kind: tkLParen}
	case '.':
		return &token{kind: tkDot}
	}
	return other
}

// skipQuoted returns the index just after the closing quote. A doubled
// closing quote is an escaped quote in every dialect.
func skipQuoted(s string, start int, closing byte, backslashEscapes bool) (int, error) {
	for i := start + 1; i < len(s); i++ {
		switch {
		case backslashEscapes && s[i] == '\\':
			i++
		case s[i] == closing:
			if i+1 < len(s) && s[i+1] == closing {
				i++
				continue
			}
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated quoted text starting at position %d", start)
}
