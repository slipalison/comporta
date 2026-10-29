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

	"dbmcp/internal/model"
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
	return analyze(dbType, stmts[0]), nil
}

func analyze(dbType model.DBType, toks []token) Analysis {
	ops := map[model.Op]bool{}
	seen := map[string]bool{}
	leading := ""
	if toks[0].kind == tkLParen {
		leading = "("
	} else if toks[0].kind == tkWord {
		leading = toks[0].text
	}

	for i, t := range toks {
		if t.kind != tkWord {
			continue
		}
		// a.update / schema.delete are identifiers, not keywords
		if i > 0 && toks[i-1].kind == tkDot {
			continue
		}
		// INSERT(...), REPLACE(...), TRUNCATE(...) are MySQL functions
		nextParen := i+1 < len(toks) && toks[i+1].kind == tkLParen
		kw := t.text
		seen[kw] = true
		switch kw {
		case "INSERT":
			if !nextParen {
				ops[model.OpInsert] = true
			}
		case "UPDATE":
			if !nextParen {
				ops[model.OpUpdate] = true
			}
		case "DELETE":
			ops[model.OpDelete] = true
		case "MERGE":
			ops[model.OpInsert], ops[model.OpUpdate], ops[model.OpDelete] = true, true, true
		case "TRUNCATE":
			if !nextParen {
				ops[model.OpDelete], ops[model.OpDDL] = true, true
			}
		}
		if slices.Contains(adminAnywhere, kw) {
			ops[model.OpDDL] = true
		}
		if dbType == model.SQLServer && slices.Contains(mssqlAdminAnywhere, kw) {
			ops[model.OpDDL] = true
		}
	}

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
	var toks []token
	n := len(s)
	nestedComments := dbType == model.Postgres || dbType == model.SQLServer
	backslashEscapes := dbType == model.MySQL

	for i := 0; i < n; {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++

		case c == '-' && i+1 < n && s[i+1] == '-', c == '#' && dbType == model.MySQL:
			for i < n && s[i] != '\n' {
				i++
			}

		case c == '/' && i+1 < n && s[i+1] == '*':
			if dbType == model.MySQL && (strings.HasPrefix(s[i:], "/*!") || strings.HasPrefix(s[i:], "/*M!")) {
				return nil, errors.New("MySQL executable comments (/*! ... */) are not allowed")
			}
			depth := 1
			i += 2
			for i < n && depth > 0 {
				switch {
				case s[i] == '*' && i+1 < n && s[i+1] == '/':
					depth--
					i += 2
				case nestedComments && s[i] == '/' && i+1 < n && s[i+1] == '*':
					depth++
					i += 2
				default:
					i++
				}
			}
			if depth > 0 {
				return nil, errors.New("unterminated /* comment")
			}

		case c == '\'':
			end, err := skipQuoted(s, i, '\'', backslashEscapes)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tkOther})
			i = end

		case c == '"':
			end, err := skipQuoted(s, i, '"', backslashEscapes)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tkOther})
			i = end

		case c == '`' && (dbType == model.MySQL || dbType == model.SQLite):
			end, err := skipQuoted(s, i, '`', false)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tkOther})
			i = end

		case c == '[' && (dbType == model.SQLServer || dbType == model.SQLite):
			end, err := skipQuoted(s, i, ']', false)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tkOther})
			i = end

		case c == '$' && dbType == model.Postgres:
			// $1 is a parameter; $tag$...$tag$ is a dollar-quoted string
			j := i + 1
			for j < n && (isWordChar(dbType, s[j]) && s[j] != '$') {
				j++
			}
			if j < n && s[j] == '$' && (j == i+1 || !(s[i+1] >= '0' && s[i+1] <= '9')) {
				tag := s[i : j+1]
				end := strings.Index(s[j+1:], tag)
				if end < 0 {
					return nil, errors.New("unterminated dollar-quoted string")
				}
				toks = append(toks, token{kind: tkOther})
				i = j + 1 + end + len(tag)
			} else {
				toks = append(toks, token{kind: tkOther})
				i = j
			}

		case isWordStart(dbType, c):
			j := i + 1
			for j < n && isWordChar(dbType, s[j]) {
				j++
			}
			toks = append(toks, token{kind: tkWord, text: strings.ToUpper(s[i:j])})
			i = j

		case c >= '0' && c <= '9':
			j := i + 1
			for j < n && (isWordChar(dbType, s[j]) || s[j] == '.') {
				j++
			}
			toks = append(toks, token{kind: tkOther})
			i = j

		case c == ';':
			toks = append(toks, token{kind: tkSemicolon})
			i++
		case c == '(':
			toks = append(toks, token{kind: tkLParen})
			i++
		case c == '.':
			toks = append(toks, token{kind: tkDot})
			i++
		default:
			toks = append(toks, token{kind: tkOther})
			i++
		}
	}
	return toks, nil
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
