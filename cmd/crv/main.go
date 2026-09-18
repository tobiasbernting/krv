// Command crv reviews diffs in the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/followup"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/gitsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
	"github.com/tobiasbernting/code-review-cli/internal/tui"
)

// Build metadata, injected by GoReleaser via -ldflags. A plain `go build` or
// `go install` sets none of it, so the defaults are filled in from the build
// info Go embeds instead — otherwise everyone who installed with go install
// sees "dev" and cannot tell which version they are running.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// pseudoVersion matches the timestamp-and-hash Go appends when it derives a
// version from a commit rather than a tag.
var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}`)

// releaseVersion accepts only a version that came from a real tag.
func releaseVersion(v string) (string, bool) {
	if v == "" || v == "(devel)" || strings.Contains(v, "+") || pseudoVersion.MatchString(v) {
		return "", false
	}
	return strings.TrimPrefix(v, "v"), true
}

func buildInfo() (string, string, string) {
	v, c, d := version, commit, date
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c, d
	}
	// Main.Version is the module version for `go install pkg@version`. Built
	// from a working tree it is "(devel)" or a pseudo-version derived from
	// the last tag — "1.1.1-0.20260903201147-848cbb466d36+dirty" — which is
	// accurate but says "dev" more usefully.
	if v == "dev" {
		if release, ok := releaseVersion(info.Main.Version); ok {
			v = release
		}
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if c == "none" && len(setting.Value) >= 7 {
				c = setting.Value[:7]
			}
		case "vcs.time":
			if d == "unknown" {
				d = setting.Value
			}
		case "vcs.modified":
			if setting.Value == "true" {
				c += "-dirty"
			}
		}
	}
	return v, c, d
}

// usage is built at call time so it can name the actual configuration paths
// on this machine rather than describing where they might be.
func usage() string {
	userPath, err := config.UserPath()
	if err != nil {
		userPath = "(could not determine your config directory)"
	}
	notesDir, err := notes.Dir()
	if err != nil {
		notesDir = "(could not determine your config directory)"
	}

	return fmt.Sprintf(`crv — review code in the terminal

usage:
  crv                the pull requests waiting on your review
  crv .              review uncommitted work (including untracked files)
  crv <range>        review a range, e.g. main...feature or HEAD~3..HEAD
  crv <number>       review a pull request, e.g. crv 42

flags:
  --host <name>      GitHub hostname (default: whatever gh is configured with)
  --theme <name>     colour theme: dark, light, high-contrast
  --syntax <name>    chroma style for code, overriding the theme's own
  --density <name>   row density: comfortable or compact
  --layout <name>    diff layout: unified or split
  --no-color         disable colour (also honours NO_COLOR)
  --no-untracked     exclude untracked files from the working-tree diff
  --width <n>        output width when not attached to a terminal
  --limit <n>        how many pull requests the queue lists (default 30)
  --export markdown  print the saved notes for this review and exit
  --config           print the resolved configuration and exit
  --init-config      write a starter configuration file and exit
  --version          print version and exit

configuration:
  Entirely optional — crv works with no configuration at all. Settings are
  read from the following, and the first one that mentions a setting wins:

    1. the flags above
    2. environment: CRV_HOST, CRV_THEME, CRV_SYNTAX, CRV_DENSITY,
       CRV_LAYOUT, CRV_EDITOR, CRV_WIDTH, CRV_UNTRACKED, CRV_COLOR,
       NO_COLOR
    3. %s in the repository being reviewed
    4. %s

  To create the user-level file, with every setting documented and
  commented out:

    crv --init-config

  Both files are TOML and every key is optional:

    host = "github.example.com"   # default: whatever gh is configured with
    theme = "dark"                # dark, light or high-contrast
    syntax = "catppuccin-mocha"   # any chroma style name
    density = "comfortable"       # comfortable or compact
    layout = "unified"            # unified or split (split needs 140 columns)
    editor = "hx"                 # default: $VISUAL, then $EDITOR, then vi
    untracked = true              # include untracked files in crv .
    color = true
    width = 120                   # used when output is piped

  Set host in a repository's %s to review on an enterprise host
  without changing anything globally.

  `+"`crv --config`"+` prints which settings are in effect and which files were
  read. Review notes are kept in:
    %s

`, config.RepoFile, userPath, config.RepoFile, notesDir)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "crv: "+err.Error())
		os.Exit(1)
	}
}

// options are the command-line flags. They are registered on a FlagSet rather
// than the global one so the help text can be checked against the flags that
// actually exist.
type options struct {
	host        string
	theme       string
	syntax      string
	density     string
	layout      string
	export      string
	noColor     bool
	noUntracked bool
	showConfig  bool
	showVersion bool
	initConfig  bool
	width       int
	limit       int
}

func registerFlags(fs *flag.FlagSet) *options {
	var o options
	fs.StringVar(&o.host, "host", "", "GitHub hostname")
	fs.StringVar(&o.theme, "theme", "", "colour theme: dark, light, high-contrast")
	fs.StringVar(&o.syntax, "syntax", "", "chroma style for code")
	fs.StringVar(&o.density, "density", "", "row density: comfortable or compact")
	fs.StringVar(&o.layout, "layout", "", "diff layout: unified or split")
	fs.StringVar(&o.export, "export", "", "print saved notes: markdown")
	fs.BoolVar(&o.noColor, "no-color", false, "disable colour")
	fs.BoolVar(&o.noUntracked, "no-untracked", false, "exclude untracked files")
	fs.BoolVar(&o.showConfig, "config", false, "print the resolved configuration")
	fs.BoolVar(&o.initConfig, "init-config", false, "write a starter configuration file")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")
	fs.IntVar(&o.width, "width", 0, "output width when not a terminal")
	fs.IntVar(&o.limit, "limit", 30, "how many pull requests the queue lists")
	return &o
}

// applyFlags lays the flags that were actually set over the loaded
// configuration.
func applyFlags(fs *flag.FlagSet, o *options, cfg *config.Config) {
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			cfg.Host = o.host
		case "theme":
			cfg.Theme = o.theme
		case "syntax":
			cfg.Syntax = o.syntax
		case "density":
			cfg.Density = o.density
		case "layout":
			cfg.Layout = o.layout
		case "no-color":
			cfg.Color = !o.noColor
		case "no-untracked":
			cfg.Untracked = !o.noUntracked
		case "width":
			cfg.Width = o.width
		}
	})
}

func run() error {
	fs := flag.NewFlagSet("crv", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage()) }
	opts := registerFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if opts.showVersion {
		v, c, d := buildInfo()
		fmt.Printf("crv %s (%s, built %s)\n", v, c, d)
		return nil
	}

	if opts.initConfig {
		return initConfig()
	}

	// Earlier versions stored everything under os.UserConfigDir, which on
	// macOS is ~/Library/Application Support. Move it once so saved notes
	// survive the change of location.
	if from, to, moved := config.Migrate(); moved {
		fmt.Fprintf(os.Stderr, "crv: moved your notes and settings\n     from %s\n     to   %s\n", from, to)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := gitsrc.Open(cwd)
	if err != nil {
		return err
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return err
	}
	// Flags are applied last: only here is it known which were actually set.
	applyFlags(fs, opts, &cfg)

	if opts.showConfig {
		return printConfig(cfg, repo.Root)
	}

	// A bare `crv` opens the queue: it is the one invocation with no natural
	// argument, and it is the thing that replaces opening github.com.
	if fs.NArg() == 0 && opts.export == "" {
		sel, err := runQueue(repo, cfg, opts.limit)
		if err != nil {
			return err
		}
		if !sel.Chosen {
			return nil
		}
		return reviewPR(repo, cfg, sel.Repo, sel.Number)
	}

	target := "."
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}

	src, files, err := resolve(repo, cfg, target)
	if err != nil {
		return err
	}

	return start(repo, cfg, src, files, opts.export)
}

// start loads the saved notes for a source and shows it, however it was
// reached: a target on the command line or a row in the queue.
func start(repo *gitsrc.Repo, cfg config.Config, src tui.Source, files []*diffparse.FileDiff, export string) error {
	branch, _ := repo.Branch()
	review, err := notes.Load(src.Scope(repo.Root, branch))
	if err != nil {
		return err
	}

	if export != "" {
		return printExport(export, review)
	}
	if len(files) == 0 && src.Kind != tui.SourcePR {
		fmt.Println("no changes")
		return nil
	}

	var threads []ghsrc.Thread
	var syncedAt time.Time
	var syncError string
	if src.FollowUp != nil {
		threads = src.FollowUp.Threads
		syncedAt = time.Now()
	} else if src.Kind == tui.SourcePR {
		// A failure here must not block the review: the diff is the point,
		// and existing discussions are additional context.
		feed, threadErr := src.Client.Threads(src.Repo, src.PRNumber)
		if threadErr != nil {
			syncError = "comments unavailable; press r to retry: " + threadErr.Error()
			fmt.Fprintln(os.Stderr, "crv: "+syncError)
		} else {
			threads = feed.Threads
			syncedAt = time.Now()
		}
	}

	th, layout, err := presentation(cfg)
	if err != nil {
		return err
	}

	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return printPlain(files, th, layout, cfg, tui.Overlay(review, threads, files, tui.OverlayOptions{Plain: true}))
	}
	_, err = tea.NewProgram(tui.New(tui.Options{
		Files:   files,
		Theme:   th,
		Layout:  layout,
		Config:  cfg,
		Source:  src,
		Review:  review,
		Threads: threads, SyncedAt: syncedAt, SyncError: syncError,
	}), screenOptions(cfg)...).Run()
	return err
}

// screenOptions are the full-screen program settings every crv screen shares.
func screenOptions(cfg config.Config) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if cfg.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	return opts
}

// runQueue shows the review queue and returns what was chosen.
func runQueue(repo *gitsrc.Repo, cfg config.Config, limit int) (tui.Selection, error) {
	client := ghsrc.Client{Host: cfg.Host, Dir: repo.Root}
	if err := client.Preflight(); err != nil {
		return tui.Selection{}, fmt.Errorf("%w\n\nthe queue needs gh; local reviews (crv . and crv <range>) do not", err)
	}

	// Piped output gets the list as text: starting a full-screen program with
	// no terminal would fail, and `crv | grep` is a reasonable thing to want.
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return tui.Selection{}, printQueue(client, limit)
	}

	th, _, err := presentation(cfg)
	if err != nil {
		return tui.Selection{}, err
	}

	model, err := tea.NewProgram(tui.NewQueue(client, th, limit), screenOptions(cfg)...).Run()
	if err != nil {
		return tui.Selection{}, err
	}
	q, ok := model.(tui.QueueModel)
	if !ok {
		return tui.Selection{}, nil
	}
	return q.Selected, nil
}

// printQueue is the non-interactive queue: one line per pull request.
func printQueue(client ghsrc.Client, limit int) error {
	items, _, err := client.CachedQueue(ghsrc.FilterReviewRequested, limit, false)
	if err != nil && len(items) == 0 {
		return err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "crv: "+err.Error()+" — showing the cached list")
	}
	if len(items) == 0 {
		fmt.Println("nothing waiting on your review")
		return nil
	}
	for _, it := range items {
		check := " "
		switch it.Checks {
		case "SUCCESS":
			check = "✓"
		case "FAILURE", "ERROR":
			check = "✗"
		case "PENDING":
			check = "•"
		}
		fmt.Printf("%s %s#%-4d %-8s %-4s %s\n", check, it.Repo, it.Number, it.Author, it.Age(), it.Title)
	}
	return nil
}

// reviewPR opens a pull request chosen from the queue. It may live in another
// repository than the working directory, so the client is pointed at that
// repository by name rather than by path.
func reviewPR(repo *gitsrc.Repo, cfg config.Config, name string, number int) error {
	client := ghsrc.Client{Host: cfg.Host, Dir: repo.Root, Repo: name}

	src, files, err := loadPR(client, name, number)
	if err != nil {
		return err
	}
	return start(repo, cfg, src, files, "")
}

var prNumber = regexp.MustCompile(`^#?(\d+)$`)

// resolve turns the command-line target into a source and its diff. A bare
// number is a pull request, "." is the working tree, anything else is handed
// to git as a revision range.
func resolve(repo *gitsrc.Repo, cfg config.Config, target string) (tui.Source, []*diffparse.FileDiff, error) {
	if m := prNumber.FindStringSubmatch(target); m != nil {
		n, _ := strconv.Atoi(m[1])
		return resolvePR(repo, cfg, n)
	}

	if target == "" || target == "." {
		files, err := repo.WorkingTree(cfg.Untracked)
		return tui.Source{Kind: tui.SourceLocal, Title: "working tree"}, files, err
	}
	files, err := repo.Range(target)
	return tui.Source{Kind: tui.SourceLocal, Title: target}, files, err
}

func resolvePR(repo *gitsrc.Repo, cfg config.Config, number int) (tui.Source, []*diffparse.FileDiff, error) {
	client := ghsrc.Client{Host: cfg.Host, Dir: repo.Root}
	if err := client.Preflight(); err != nil {
		if errors.Is(err, ghsrc.ErrNotInstalled) {
			return tui.Source{}, nil, fmt.Errorf("%w\n\nlocal reviews (crv . and crv <range>) work without it", err)
		}
		return tui.Source{}, nil, err
	}

	name, err := client.CurrentRepo()
	if err != nil {
		return tui.Source{}, nil, err
	}
	return loadPR(client, name, number)
}

func loadPR(client ghsrc.Client, name string, number int) (tui.Source, []*diffparse.FileDiff, error) {
	client.Repo = name
	snapshot, err := client.ReviewSnapshot(name, number)
	if err != nil {
		return tui.Source{}, nil, err
	}
	session := followup.FromSnapshot(snapshot)
	pr := session.PR
	src := tui.Source{
		Kind:  tui.SourcePR,
		Title: fmt.Sprintf("%s#%d %s", name, pr.Number, pr.Title),
		Repo:  name, PRNumber: pr.Number, Client: client,
		Author: pr.Author.Login, Viewer: session.Viewer,
		HeadSHA: pr.HeadSHA, FollowUp: session,
	}
	return src, session.Files, nil
}

func printExport(format string, review *notes.Review) error {
	if format != "markdown" && format != "md" {
		return fmt.Errorf("unknown export format %q — only markdown is supported", format)
	}
	md := review.Markdown()
	if md == "" {
		fmt.Fprintln(os.Stderr, "crv: no notes for this review")
		return nil
	}
	_, err := os.Stdout.WriteString(md)
	return err
}

func printConfig(cfg config.Config, repoRoot string) error {
	userPath, _ := config.UserPath()
	fmt.Printf("host       %s\n", orDefault(cfg.Host, "(gh's own configuration)"))
	fmt.Printf("theme      %s\n", cfg.Theme)
	th, layout, err := presentation(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("syntax     %s%s\n", th.Syntax, source(cfg.Syntax != "", "the theme's own"))
	fmt.Printf("density    %s\n", layout.Density)
	fmt.Printf("layout     %s\n", layout.Mode)
	fmt.Printf("editor     %s\n", cfg.EditorCommand())
	fmt.Printf("untracked  %t\n", cfg.Untracked)
	fmt.Printf("color      %t\n", cfg.Color)
	fmt.Printf("mouse      %t\n", cfg.Mouse)
	fmt.Printf("width      %d\n", cfg.Width)
	fmt.Printf("\nuser file  %s%s\n", userPath, exists(userPath))
	repoFile := filepath.Join(repoRoot, config.RepoFile)
	fmt.Printf("repo file  %s%s\n", repoFile, exists(repoFile))
	if s := cfg.Sources(); len(s) > 0 {
		fmt.Printf("loaded     %s\n", strings.Join(s, ", "))
	} else {
		fmt.Printf("loaded     (none — all defaults; crv --help shows how to create one)\n")
	}
	dir, _ := notes.Dir()
	fmt.Printf("notes      %s\n", dir)
	return nil
}

// presentation resolves the configured names into the theme and layout the
// renderer works in. A theme name that is not one of crv's own but is a chroma
// style is taken as a syntax style over the dark theme: that is what the
// setting meant before crv had themes, and a configuration file that used to
// work should keep working.
func presentation(cfg config.Config) (render.Theme, render.Layout, error) {
	th, ok := render.ThemeByName(cfg.Theme)
	switch {
	case ok:
	case cfg.Theme == "":
		th = render.DefaultTheme()
	case render.KnownSyntax(cfg.Theme):
		th = render.DefaultTheme()
		th.Syntax = cfg.Theme
	default:
		return th, render.Layout{}, fmt.Errorf("unknown theme %q — themes are %s, or any chroma style name",
			cfg.Theme, strings.Join(render.ThemeNames(), ", "))
	}
	if cfg.Syntax != "" {
		if !render.KnownSyntax(cfg.Syntax) {
			return th, render.Layout{}, fmt.Errorf("unknown syntax style %q — see https://xyproto.github.io/splash/docs/", cfg.Syntax)
		}
		th.Syntax = cfg.Syntax
	}
	density, ok := render.ParseDensity(cfg.Density)
	if !ok {
		return th, render.Layout{}, fmt.Errorf("unknown density %q — use comfortable or compact", cfg.Density)
	}
	mode, ok := render.ParseMode(cfg.Layout)
	if !ok {
		return th, render.Layout{}, fmt.Errorf("unknown layout %q — use unified or split", cfg.Layout)
	}
	return th, render.Layout{Density: density, Mode: mode}, nil
}

// source annotates a resolved value that the user did not set themselves.
func source(explicit bool, from string) string {
	if explicit {
		return ""
	}
	return "  (" + from + ")"
}

// initConfig writes the starter file, or explains why it did not.
func initConfig() error {
	path, err := config.Init()
	switch {
	case errors.Is(err, config.ErrConfigExists):
		fmt.Printf("configuration already exists: %s\n", path)
		fmt.Println("edit it, or delete it and run this again for a fresh copy")
		return nil
	case err != nil:
		return err
	}
	fmt.Printf("wrote %s\n\n", path)
	fmt.Println("every setting is commented out, so nothing is overridden until you")
	fmt.Println("uncomment it — crv keeps using its own defaults, including if they change.")
	return nil
}

// exists annotates a path with whether a file is actually there, so --config
// answers "is my file being picked up?" and not only "where would it go?".
func exists(path string) string {
	if _, err := os.Stat(path); err == nil {
		return ""
	}
	return "  (does not exist)"
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// printPlain is the non-TTY path: same rows, printed once and exited, so
// `crv . | less` and `crv . > review.txt` work.
func printPlain(files []*diffparse.FileDiff, th render.Theme, layout render.Layout, cfg config.Config, ov render.Overlay) error {
	width := cfg.Width
	if width <= 0 {
		width = 120
	}
	// Piped output has a fixed width, so a split layout that would not fit
	// quietly becomes unified rather than printing unreadable panes.
	layout = layout.Fit(width)
	doc := render.Build(files, render.NewHighlighter(th.Syntax, cfg.Color), ov, layout)
	r := render.NewRenderer(th, doc)

	var b strings.Builder
	for _, row := range doc.Rows {
		for _, line := range r.RenderLines(row, width, 0, false, 0) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	_, err := os.Stdout.WriteString(b.String())
	return err
}
