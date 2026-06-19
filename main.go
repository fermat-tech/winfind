package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- program name ----

var progName string

func init() {
	progName = strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
}

// ---- predicate types ----

type predicate func(path string, info fs.FileInfo) bool

// ---- parser state ----

type parser struct {
	args []string
	pos  int
}

func (p *parser) peek() (string, bool) {
	if p.pos >= len(p.args) {
		return "", false
	}
	return p.args[p.pos], true
}

func (p *parser) next() (string, bool) {
	s, ok := p.peek()
	if ok {
		p.pos++
	}
	return s, ok
}

func (p *parser) require(flag string) string {
	v, ok := p.next()
	if !ok || strings.HasPrefix(v, "-") {
		fatal("-%s requires an argument", flag)
	}
	return v
}

// ---- size parsing ----

func parseSize(s string) (int64, int) { // returns bytes, modifier (+1 0 -1)
	mod := 0
	if strings.HasPrefix(s, "+") {
		mod = 1
		s = s[1:]
	} else if strings.HasPrefix(s, "-") {
		mod = -1
		s = s[1:]
	}
	if len(s) == 0 {
		fatal("-size: empty size value")
	}
	suffix := s[len(s)-1]
	numStr := s
	mult := int64(512) // default unit is 512-byte blocks
	switch suffix {
	case 'c':
		mult = 1
		numStr = s[:len(s)-1]
	case 'k':
		mult = 1024
		numStr = s[:len(s)-1]
	case 'M':
		mult = 1024 * 1024
		numStr = s[:len(s)-1]
	case 'G':
		mult = 1024 * 1024 * 1024
		numStr = s[:len(s)-1]
	default:
		if suffix >= '0' && suffix <= '9' {
			// plain number = 512-byte blocks
		} else {
			fatal("-size: unknown suffix %q", string(suffix))
		}
	}
	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		fatal("-size: invalid number %q", numStr)
	}
	return n * mult, mod
}

// ---- mtime parsing ----

func parseDays(s string) (float64, int) {
	mod := 0
	if strings.HasPrefix(s, "+") {
		mod = 1
		s = s[1:]
	} else if strings.HasPrefix(s, "-") {
		mod = -1
		s = s[1:]
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		fatal("-mtime: invalid number %q", s)
	}
	return n, mod
}

// ---- exec template expansion ----

func buildExecArgs(template []string, path string) []string {
	out := make([]string, len(template))
	for i, t := range template {
		out[i] = strings.ReplaceAll(t, "{}", path)
	}
	return out
}

// ---- predicate builder ----

func (p *parser) parsePrimary(roots []string) predicate {
	tok, ok := p.peek()
	if !ok {
		return nil
	}

	switch tok {
	case "-not", "!":
		p.pos++
		inner := p.parsePrimary(roots)
		if inner == nil {
			fatal("%s requires an expression", tok)
		}
		return func(path string, info fs.FileInfo) bool { return !inner(path, info) }

	case "(":
		p.pos++
		pred := p.parseExpr(roots)
		if t, ok2 := p.next(); !ok2 || t != ")" {
			fatal("missing closing ')'")
		}
		return pred

	case "-name":
		p.pos++
		pat := p.require("name")
		return func(path string, info fs.FileInfo) bool {
			matched, _ := filepath.Match(pat, info.Name())
			return matched
		}

	case "-iname":
		p.pos++
		pat := strings.ToLower(p.require("iname"))
		return func(path string, info fs.FileInfo) bool {
			matched, _ := filepath.Match(pat, strings.ToLower(info.Name()))
			return matched
		}

	case "-path":
		p.pos++
		pat := p.require("path")
		return func(path string, info fs.FileInfo) bool {
			matched, _ := filepath.Match(pat, filepath.ToSlash(path))
			return matched
		}

	case "-ipath":
		p.pos++
		pat := strings.ToLower(p.require("ipath"))
		return func(path string, info fs.FileInfo) bool {
			matched, _ := filepath.Match(pat, strings.ToLower(filepath.ToSlash(path)))
			return matched
		}

	case "-type":
		p.pos++
		t := p.require("type")
		return func(path string, info fs.FileInfo) bool {
			switch t {
			case "f":
				return info.Mode().IsRegular()
			case "d":
				return info.IsDir()
			case "l":
				return info.Mode()&fs.ModeSymlink != 0
			default:
				fatal("-type: unknown type %q (use f, d, l)", t)
				return false
			}
		}

	case "-size":
		p.pos++
		bytes, mod := parseSize(p.require("size"))
		return func(path string, info fs.FileInfo) bool {
			sz := info.Size()
			switch mod {
			case 1:
				return sz > bytes
			case -1:
				return sz < bytes
			default:
				return sz == bytes
			}
		}

	case "-mtime":
		p.pos++
		days, mod := parseDays(p.require("mtime"))
		now := time.Now()
		return func(path string, info fs.FileInfo) bool {
			age := now.Sub(info.ModTime()).Hours() / 24
			switch mod {
			case 1:
				return age > days
			case -1:
				return age < days
			default:
				return math.Abs(age-days) < 1
			}
		}

	case "-newer":
		p.pos++
		ref := p.require("newer")
		fi, err := os.Stat(ref)
		if err != nil {
			fatal("-newer: cannot stat %q: %v", ref, err)
		}
		refTime := fi.ModTime()
		return func(path string, info fs.FileInfo) bool {
			return info.ModTime().After(refTime)
		}

	case "-empty":
		p.pos++
		return func(path string, info fs.FileInfo) bool {
			if info.IsDir() {
				entries, err := os.ReadDir(path)
				return err == nil && len(entries) == 0
			}
			return info.Size() == 0
		}

	case "-print", "-print0", "-ls", "-delete":
		// actions — handled as always-true predicates with side effects below
		// they are parsed as part of the action collection, not here
		return nil

	case "-exec":
		// also handled separately
		return nil

	default:
		// not a recognised predicate token; stop
		return nil
	}
}

func (p *parser) parseExpr(roots []string) predicate {
	left := p.parseAnd(roots)
	for {
		tok, ok := p.peek()
		if !ok || (tok != "-or" && tok != "-o") {
			break
		}
		p.pos++
		right := p.parseAnd(roots)
		l, r := left, right
		left = func(path string, info fs.FileInfo) bool {
			return l(path, info) || r(path, info)
		}
	}
	return left
}

func (p *parser) parseAnd(roots []string) predicate {
	left := p.parsePrimary(roots)
	if left == nil {
		return alwaysTrue
	}
	for {
		tok, ok := p.peek()
		if !ok {
			break
		}
		// consume optional explicit -and / -a
		if tok == "-and" || tok == "-a" {
			p.pos++
		}
		// check if next token is a predicate
		right := p.parsePrimary(roots)
		if right == nil {
			break
		}
		l, r := left, right
		left = func(path string, info fs.FileInfo) bool {
			return l(path, info) && r(path, info)
		}
	}
	return left
}

func alwaysTrue(_ string, _ fs.FileInfo) bool { return true }

// ---- action collection ----

type action struct {
	kind     string   // print, print0, ls, delete, exec
	execArgs []string // for exec
}

func collectActions(args []string) ([]action, []string) {
	var actions []action
	var remaining []string
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-print":
			actions = append(actions, action{kind: "print"})
			i++
		case "-print0":
			actions = append(actions, action{kind: "print0"})
			i++
		case "-ls":
			actions = append(actions, action{kind: "ls"})
			i++
		case "-delete":
			actions = append(actions, action{kind: "delete"})
			i++
		case "-exec":
			i++
			var tmpl []string
			for i < len(args) && args[i] != ";" {
				tmpl = append(tmpl, args[i])
				i++
			}
			if i < len(args) {
				i++ // consume ";"
			}
			actions = append(actions, action{kind: "exec", execArgs: tmpl})
		default:
			remaining = append(remaining, args[i])
			i++
		}
	}
	return actions, remaining
}

// ---- walk ----

type walker struct {
	roots    []string
	pred     predicate
	actions  []action
	maxDepth int
	minDepth int
}

func (w *walker) run() {
	for _, root := range w.roots {
		root = filepath.Clean(root)
		rootDepth := strings.Count(root, string(os.PathSeparator))
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s: %v\n", progName, path, err)
				return nil
			}
			depth := strings.Count(path, string(os.PathSeparator)) - rootDepth

			if w.maxDepth >= 0 && depth > w.maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if depth < w.minDepth {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s: %v\n", progName, path, err)
				return nil
			}

			if w.pred(path, info) {
				w.dispatch(path, info)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", progName, err)
		}
	}
}

func (w *walker) dispatch(path string, info fs.FileInfo) {
	if len(w.actions) == 0 {
		fmt.Println(path)
		return
	}
	for _, a := range w.actions {
		switch a.kind {
		case "print":
			fmt.Println(path)
		case "print0":
			fmt.Print(path)
			os.Stdout.Write([]byte{0})
		case "ls":
			fmt.Printf("%10d %s %s %s\n",
				info.Size(),
				info.Mode(),
				info.ModTime().Format("2006-01-02 15:04"),
				path,
			)
		case "delete":
			var err error
			if info.IsDir() {
				err = os.Remove(path)
			} else {
				err = os.Remove(path)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: delete %s: %v\n", progName, path, err)
			}
		case "exec":
			args := buildExecArgs(a.execArgs, path)
			if len(args) == 0 {
				continue
			}
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "%s: exec: %v\n", progName, err)
			}
		}
	}
}

// ---- depth flag parsing ----

func extractDepths(args []string) (maxDepth, minDepth int, rest []string) {
	maxDepth = -1 // -1 = unlimited
	minDepth = 0
	for i := 0; i < len(args); {
		switch args[i] {
		case "-maxdepth":
			i++
			if i >= len(args) {
				fatal("-maxdepth requires an argument")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fatal("-maxdepth: invalid value %q", args[i])
			}
			maxDepth = n
			i++
		case "-mindepth":
			i++
			if i >= len(args) {
				fatal("-mindepth requires an argument")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fatal("-mindepth: invalid value %q", args[i])
			}
			minDepth = n
			i++
		default:
			rest = append(rest, args[i])
			i++
		}
	}
	return
}

// ---- root / expression splitting ----

// Tokens that start an expression (not paths)
var exprTokens = map[string]bool{
	"-name": true, "-iname": true, "-path": true, "-ipath": true,
	"-type": true, "-size": true, "-mtime": true, "-newer": true,
	"-empty": true, "-not": true, "!": true, "(": true,
	"-and": true, "-a": true, "-or": true, "-o": true,
	"-print": true, "-print0": true, "-ls": true, "-delete": true,
	"-exec": true, "-maxdepth": true, "-mindepth": true,
}

func splitRootsAndArgs(args []string) (roots, exprArgs []string) {
	i := 0
	for i < len(args) {
		if _, isExpr := exprTokens[args[i]]; isExpr {
			break
		}
		// Looks like a path (doesn't start with - and not a special token)
		if strings.HasPrefix(args[i], "-") {
			break
		}
		roots = append(roots, args[i])
		i++
	}
	exprArgs = args[i:]
	if len(roots) == 0 {
		roots = []string{"."}
	}
	return
}

// ---- fatal ----

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, progName+": "+format+"\n", args...)
	os.Exit(1)
}

// ---- usage ----

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: %s [path...] [expression]

Paths default to . if omitted.

Predicates:
  -name PATTERN       filename glob (case-sensitive)
  -iname PATTERN      filename glob (case-insensitive)
  -path PATTERN       full path glob
  -ipath PATTERN      full path glob (case-insensitive)
  -type f|d|l         file, directory, or symlink
  -size [+/-]N[ckMG]  size: c=bytes k=KB M=MB G=GB; +N>N, -N<N
  -mtime [+/-]N       modified N days ago; +N older, -N newer
  -newer FILE         modified more recently than FILE
  -empty              empty file or empty directory
  -maxdepth N         descend at most N levels
  -mindepth N         skip entries less than N levels deep

Operators (higher to lower precedence):
  ! PRED  /  -not PRED
  PRED1 PRED2          (implicit -and)
  PRED1 -and PRED2
  PRED1 -or  PRED2

Actions (default: -print):
  -print              print path followed by newline
  -print0             print path followed by null byte
  -ls                 print size, mode, mtime, path
  -delete             delete matched file or empty directory
  -exec CMD {} \;     run CMD for each match ({} is replaced by path)

Examples:
  %s . -name "*.go"
  %s C:\src -type f -size +1M
  %s . -mtime -7 -name "*.log" -delete
  %s . -not -type d -exec file {} ;
`, progName, progName, progName, progName, progName)
	os.Exit(0)
}

// ---- main ----

func main() {
	args := os.Args[1:]

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
	}

	roots, exprArgs := splitRootsAndArgs(args)

	// Pull depth flags out first (they aren't predicates)
	maxDepth, minDepth, exprArgs := extractDepths(exprArgs)

	// Separate action flags from predicate flags
	actions, predArgs := collectActions(exprArgs)

	// Parse predicates
	p := &parser{args: predArgs}
	pred := p.parseExpr(roots)
	if pred == nil {
		pred = alwaysTrue
	}
	if t, ok := p.peek(); ok {
		fatal("unexpected token %q", t)
	}

	w := &walker{
		roots:    roots,
		pred:     pred,
		actions:  actions,
		maxDepth: maxDepth,
		minDepth: minDepth,
	}
	w.run()
}
