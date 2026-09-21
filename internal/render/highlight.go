package render

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Segment is a run of text sharing one foreground colour. Background is left
// to the diff renderer, which owns add/delete/word-change shading.
type Segment struct {
	Text string
	Fg   string // "#rrggbb", or "" for the terminal default

	// Emph marks the tokens that carry the most meaning when skimming code —
	// function, method, class and type names. The renderer mutes syntax so
	// diff state wins the page, and mutes these less than the rest.
	Emph bool
}

// Highlighter tokenises source with chroma. It highlights a whole hunk side at
// once rather than line by line, so multi-line constructs (block comments,
// raw strings) colour correctly.
type Highlighter struct {
	style   *chroma.Style
	enabled bool

	mu     sync.Mutex
	lexers map[string]chroma.Lexer

	// cache memoises highlighting per (path, source). The document is rebuilt
	// whenever a note is added or a file is marked reviewed, and re-lexing a
	// large pull request on every keystroke is the difference between instant
	// and unusable.
	cache map[string][][]Segment
}

func NewHighlighter(styleName string, enabled bool) *Highlighter {
	st := styles.Get(styleName)
	if st == nil {
		st = styles.Fallback
	}
	return &Highlighter{
		style:   st,
		enabled: enabled,
		lexers:  map[string]chroma.Lexer{},
		cache:   map[string][][]Segment{},
	}
}

// Lines highlights source and returns one segment slice per line. The result
// always has exactly as many entries as source has lines.
func (h *Highlighter) Lines(path, source string) [][]Segment {
	return h.lines(path, source, func() chroma.Lexer { return lexers.Match(path) })
}

// Lang highlights source written in a named language — a Markdown fence's
// info string, such as "go" or "python" — rather than guessing it from a file
// name. An unknown name is tried as a file extension, then as plain text.
func (h *Highlighter) Lang(lang, source string) [][]Segment {
	lang = strings.ToLower(strings.TrimSpace(lang))
	return h.lines("\x00lang\x00"+lang, source, func() chroma.Lexer {
		if lang == "" {
			return nil
		}
		if l := lexers.Get(lang); l != nil {
			return l
		}
		return lexers.Match("file." + lang)
	})
}

// lines tokenises source with the lexer find returns, memoised under lexKey.
// A nil Highlighter highlights nothing, so callers need not check.
func (h *Highlighter) lines(lexKey, source string, find func() chroma.Lexer) [][]Segment {
	plain := splitPlain(source)
	if h == nil || !h.enabled {
		return plain
	}

	key := lexKey + "\x00" + source
	h.mu.Lock()
	cached, ok := h.cache[key]
	h.mu.Unlock()
	if ok {
		return cached
	}

	it, err := h.lexerFor(lexKey, find).Tokenise(nil, source)
	if err != nil {
		return plain
	}

	out := [][]Segment{{}}
	for tok := it(); tok != chroma.EOF; tok = it() {
		fg := ""
		if e := h.style.Get(tok.Type); e.Colour.IsSet() {
			fg = e.Colour.String()
		}
		emph := emphasised(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				out = append(out, []Segment{})
			}
			if p != "" {
				last := len(out) - 1
				out[last] = append(out[last], Segment{Text: p, Fg: fg, Emph: emph})
			}
		}
	}
	// Tokenise appends a trailing newline to input that lacks one; drop the
	// empty line it produces so the count matches the source.
	if len(out) > len(plain) {
		out = out[:len(plain)]
	}
	for len(out) < len(plain) {
		out = append(out, []Segment{})
	}

	h.mu.Lock()
	h.cache[key] = out
	h.mu.Unlock()
	return out
}

// emphasised reports whether a token type names something — a function, a
// type, a class — as opposed to punctuation, keywords or literals. Names are
// what the eye looks for when scanning an unfamiliar diff, so they keep more
// of their colour when the rest of the syntax is muted.
func emphasised(t chroma.TokenType) bool {
	switch t {
	case chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameClass,
		chroma.NameNamespace, chroma.NameException, chroma.NameDecorator,
		chroma.NameTag, chroma.NameBuiltin, chroma.NameBuiltinPseudo,
		chroma.KeywordType:
		return true
	}
	return false
}

func (h *Highlighter) lexerFor(key string, find func() chroma.Lexer) chroma.Lexer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l, ok := h.lexers[key]; ok {
		return l
	}
	l := find()
	if l == nil {
		l = lexers.Fallback
	}
	l = chroma.Coalesce(l)
	h.lexers[key] = l
	return l
}

func splitPlain(source string) [][]Segment {
	lines := strings.Split(source, "\n")
	out := make([][]Segment, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = []Segment{}
			continue
		}
		out[i] = []Segment{{Text: l}}
	}
	return out
}
