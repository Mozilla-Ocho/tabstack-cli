package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/schemas"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// indexCacheTTL is how long a cached copy of the library index.json is trusted
// before we refetch. `--refresh` forces a fetch regardless.
const indexCacheTTL = time.Hour

// completionTimeout bounds the index fetch behind <TAB> completion. Completion
// must feel instant, so it gets a far tighter deadline than the fetcher's own
// default: a slow network falls back to no completions rather than a hung shell.
const completionTimeout = 2 * time.Second

// statusTimeout bounds the whole `schema status` remote fan-out. Each fetch
// already carries the fetcher's own 30s per-request timeout, but a large store
// (many schemas, capped concurrency) could still stack up well past that. This
// caps the total wait so the command cannot hang; timed-out fetches surface as
// "remote unknown" rather than up to date.
const statusTimeout = 60 * time.Second

// newSchemaCmd is the parent grouping for the schema library commands. These do
// not talk to the authenticated API (they pull public files from GitHub), so
// the subcommands carry the skipClient annotation and run with a renderer only.
func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "List, pull, and manage pre-defined extraction schemas",
		Long: "Work with the public tabstack-schemas library: list the available\n" +
			"schemas, pull them into a local store, and check or remove pulled copies.\n" +
			"Pulled schemas feed `tabstack extract json --schema-name`.",
		Example: "  tabstack schema list\n  tabstack schema pull job-posting\n  tabstack extract json https://example.com/jobs/1 --schema-name job-posting",
	}
	cmd.AddCommand(
		newSchemaListCmd(),
		newSchemaPullCmd(),
		newSchemaStatusCmd(),
		newSchemaPathCmd(),
		newSchemaRmCmd(),
	)
	return cmd
}

func newSchemaListCmd() *cobra.Command {
	var (
		storage string
		local   bool
		refresh bool
	)

	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List schemas in the library (or just the ones pulled locally)",
		Example:     "  # Everything in the public library, grouped by category\n  tabstack schema list\n\n  # Only what you have pulled, without touching the network\n  tabstack schema list --local\n\n  # Ignore the cached index and refetch it\n  tabstack schema list --refresh",
		Annotations: map[string]string{"skipClient": "true"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := schemaStoreDir(storage)
			if err != nil {
				return withCode(1, err)
			}

			pulled, err := schemas.ListLocal(dir)
			if err != nil {
				return withCode(1, err)
			}

			if local {
				return printLocalList(pulled)
			}

			idx, err := schemas.CachedIndex(cmd.Context(), schemas.NewFetcher(), dir, indexCacheTTL, refresh)
			if err != nil {
				return classifyError(err)
			}
			return printSchemaList(idx, toSet(pulled))
		},
	}

	f := cmd.Flags()
	f.StringVar(&storage, "storage", "", "schema store directory (default: config dir)")
	f.BoolVar(&local, "local", false, "list only schemas pulled into the local store (offline)")
	f.BoolVar(&refresh, "refresh", false, "bypass the cached library index and refetch")
	return cmd
}

func newSchemaPullCmd() *cobra.Command {
	var (
		storage string
		all     bool
		force   bool
		refresh bool
	)

	cmd := &cobra.Command{
		Use:         "pull [selector...]",
		Short:       "Pull schemas from the library into a local store",
		Example:     "  # By bare name, by category, or by full repo path\n  tabstack schema pull job-posting\n  tabstack schema pull jobs\n  tabstack schema pull jobs/job-posting.json\n\n  # Everything, overwriting local edits without asking\n  tabstack schema pull --all --force\n\n  # Keep a project-local store instead of the default\n  tabstack schema pull job-posting --storage ./schemas",
		Annotations: map[string]string{"skipClient": "true"},
		Long: "Pull one or more schemas into a local store (default\n" +
			"$XDG_CONFIG_HOME/tabstack/schemas, or ~/.config/tabstack/schemas).\n\n" +
			"A selector is a schema name (job-posting), a category (jobs), or a full\n" +
			"repo path (jobs/job-posting.json). Use --all to pull everything.\n\n" +
			"When a schema already exists locally and differs from the remote, you\n" +
			"are prompted to overwrite, keep your local copy, or quit. Use --force to\n" +
			"overwrite without prompting. Customise a pulled schema, then re-pull only\n" +
			"when you want the latest upstream version.",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completePullSelectors,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return withCode(2, fmt.Errorf("pass selectors or --all, not both"))
			}
			if !all && len(args) == 0 {
				return withCode(2, fmt.Errorf("required: one or more selectors, or --all (run `tabstack schema list`)"))
			}

			dir, err := schemaStoreDir(storage)
			if err != nil {
				return withCode(1, err)
			}

			ctx := cmd.Context()
			fetcher := schemas.NewFetcher()

			idx, err := schemas.CachedIndex(ctx, fetcher, dir, indexCacheTTL, refresh)
			if err != nil {
				return classifyError(err)
			}

			targets, err := selectTargets(idx, args, all)
			if err != nil {
				return withCode(2, err)
			}

			return runPull(ctx, fetcher, dir, targets, force)
		},
	}

	f := cmd.Flags()
	f.StringVar(&storage, "storage", "", "directory to store schemas in (default: config dir)")
	f.BoolVar(&all, "all", false, "pull every schema in the library")
	f.BoolVar(&force, "force", false, "overwrite local changes without prompting")
	f.BoolVar(&refresh, "refresh", false, "bypass the cached library index and refetch")
	return cmd
}

func newSchemaStatusCmd() *cobra.Command {
	var (
		storage string
		local   bool
	)

	cmd := &cobra.Command{
		Use:         "status",
		Short:       "Show which pulled schemas are modified or out of date",
		Example:     "  # What have I changed, and what has moved upstream?\n  tabstack schema status\n\n  # Skip the network; only report local edits\n  tabstack schema status --local",
		Annotations: map[string]string{"skipClient": "true"},
		Args:        cobra.NoArgs,
		Long: "Report the state of each pulled schema by comparing it against the\n" +
			"content recorded when it was pulled. Local edits show as 'modified';\n" +
			"upstream changes show as 'outdated'. Use --local to skip the network and\n" +
			"only detect local edits.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := schemaStoreDir(storage)
			if err != nil {
				return withCode(1, err)
			}
			return runStatus(cmd.Context(), dir, local)
		},
	}

	f := cmd.Flags()
	f.StringVar(&storage, "storage", "", "schema store directory (default: config dir)")
	f.BoolVar(&local, "local", false, "skip the network; only detect local edits")
	return cmd
}

func newSchemaPathCmd() *cobra.Command {
	var storage string

	cmd := &cobra.Command{
		Use:               "path <name>",
		Short:             "Print the local file path of a pulled schema",
		Example:           "  # Print a stored schema's path, handy for other tools\n  tabstack schema path job-posting\n  cat \"$(tabstack schema path job-posting)\"",
		Annotations:       map[string]string{"skipClient": "true"},
		Args:              exactArgsNamed("<name>"),
		ValidArgsFunction: completeLocalSchemaNames,
		Long: "Resolve a pulled schema to its local file path, for scripting:\n" +
			"  tabstack extract json <url> --schema @\"$(tabstack schema path job-posting)\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := schemaStoreDir(storage)
			if err != nil {
				return withCode(1, err)
			}
			rel, err := schemas.FindLocal(dir, args[0])
			if err != nil {
				return withCode(2, err)
			}
			fmt.Fprintln(rootApp.renderer.Out, schemas.LocalPath(dir, rel))
			return nil
		},
	}

	cmd.Flags().StringVar(&storage, "storage", "", "schema store directory (default: config dir)")
	return cmd
}

func newSchemaRmCmd() *cobra.Command {
	var storage string

	cmd := &cobra.Command{
		Use:               "rm <selector...>",
		Short:             "Remove pulled schemas from the local store",
		Example:           "  tabstack schema rm job-posting\n  tabstack schema rm jobs/job-posting.json finance/crypto-asset.json",
		Annotations:       map[string]string{"skipClient": "true"},
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeLocalSchemaNames,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			dir, err := schemaStoreDir(storage)
			if err != nil {
				return withCode(1, err)
			}

			m, err := schemas.LoadManifest(dir)
			if err != nil {
				return withCode(1, err)
			}
			// Persist the manifest on every exit path so a mid-loop os.Remove
			// failure cannot leave already-deleted files tracked (they would
			// otherwise read as 'missing' forever). The save error only surfaces
			// if nothing else already failed.
			defer func() {
				if saveErr := m.Save(dir); saveErr != nil && err == nil {
					err = withCode(1, saveErr)
				}
			}()

			r := rootApp.renderer
			removed := []string{}
			for _, sel := range args {
				rel, err := schemas.FindLocal(dir, sel)
				if err != nil {
					return withCode(2, err)
				}
				if err := os.Remove(schemas.LocalPath(dir, rel)); err != nil {
					return withCode(1, err)
				}
				m.Remove(rel)
				removed = append(removed, rel)
				// Progress to stderr, matching pull; the tally below is the result.
				fmt.Fprintf(r.Err, "%s removed %s\n", r.Styles.Success.Render("✓"), rel)
			}

			if jsonMode(r) {
				return emitJSON(r, rmResult{Removed: removed})
			}
			fmt.Fprintf(r.Out, "\n%d removed\n", len(removed))
			return nil
		},
	}

	cmd.Flags().StringVar(&storage, "storage", "", "schema store directory (default: config dir)")
	return cmd
}

// selectTargets resolves CLI selectors into a deduplicated, ordered list of
// schema entries. With all, every schema is returned.
func selectTargets(idx schemas.Index, args []string, all bool) ([]schemas.Entry, error) {
	if all {
		return idx.Schemas, nil
	}
	seen := make(map[string]bool)
	var out []schemas.Entry
	for _, sel := range args {
		entries, err := idx.Resolve(sel)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !seen[e.Path] {
				seen[e.Path] = true
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// runPull fetches each target and reconciles it with the local store, prompting
// on conflicts. It records what was pulled in the store manifest so `schema
// status` can later distinguish local edits from upstream changes.
func runPull(ctx context.Context, fetcher *schemas.Fetcher, dir string, targets []schemas.Entry, force bool) (err error) {
	r := rootApp.renderer

	m, err := schemas.LoadManifest(dir)
	if err != nil {
		return withCode(1, err)
	}
	// Persist the manifest on every exit path so partial progress and backfilled
	// hashes survive a mid-run failure. The save error only surfaces if nothing
	// else already failed.
	defer func() {
		if saveErr := m.Save(dir); saveErr != nil && err == nil {
			err = withCode(1, saveErr)
		}
	}()

	stamp := time.Now().UTC().Format(time.RFC3339)

	// Per-schema progress goes to stderr in both modes: it is progress, not a
	// result, and leaving it on stdout meant `schema pull | ...` carried tick
	// marks. The summary below is the result and stays on stdout.
	var res pullResult
	record := func(p string, data []byte) { m.Set(p, schemas.CanonicalSHA(data), stamp) }

	for _, e := range targets {
		remote, err := fetcher.Fetch(ctx, e.Path)
		if err != nil {
			return classifyError(err)
		}

		local, exists, err := schemas.Read(dir, e.Path)
		if err != nil {
			return withCode(1, err)
		}

		switch {
		case !exists:
			if err := schemas.Write(dir, e.Path, remote); err != nil {
				return withCode(1, err)
			}
			record(e.Path, remote)
			res.Pulled = append(res.Pulled, e.Path)
			fmt.Fprintf(r.Err, "%s pulled %s\n", r.Styles.Success.Render("✓"), e.Path)

		case schemas.Equal(local, remote):
			record(e.Path, remote) // backfill manifest for pre-existing files
			res.UpToDate = append(res.UpToDate, e.Path)
			fmt.Fprintf(r.Err, "%s up to date %s\n", r.Styles.Muted.Render("="), e.Path)

		case force:
			if err := schemas.Write(dir, e.Path, remote); err != nil {
				return withCode(1, err)
			}
			record(e.Path, remote)
			res.Pulled = append(res.Pulled, e.Path)
			fmt.Fprintf(r.Err, "%s updated %s\n", r.Styles.Success.Render("✓"), e.Path)

		default:
			action, err := promptChoice(
				fmt.Sprintf("%s differs from the library. [o]verwrite, [k]eep, [q]uit? (k) ", e.Path),
				"okq", 'k')
			if err == errNotTerminal {
				return withCode(2, fmt.Errorf("%s differs from the library; re-run with --force to overwrite, or pull to a clean directory", e.Path))
			}
			if err != nil {
				return withCode(1, err)
			}
			switch action {
			case 'o':
				if err := schemas.Write(dir, e.Path, remote); err != nil {
					return withCode(1, err)
				}
				record(e.Path, remote)
				res.Pulled = append(res.Pulled, e.Path)
				fmt.Fprintf(r.Err, "%s updated %s\n", r.Styles.Success.Render("✓"), e.Path)
			case 'q':
				fmt.Fprintln(r.Err, "Quit. No further schemas pulled.")
				return printPullSummary(res)
			default: // keep
				res.Kept = append(res.Kept, e.Path)
				fmt.Fprintf(r.Err, "%s kept %s\n", r.Styles.Muted.Render("·"), e.Path)
			}
		}
	}

	return printPullSummary(res)
}

// rmResult is what `schema rm` deleted.
type rmResult struct {
	Removed []string `json:"removed"`
}

// localEntryJSON is one row of `schema list --local`. It deliberately shares
// the "path" key with the library listing's entries so both are queryable the
// same way; the descriptive fields only exist in the online index.
type localEntryJSON struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// statusRow is one schema's reconciled state for `schema status`.
type statusRow struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// runStatus computes and prints the state of each pulled schema. With local it
// skips the network and only detects local edits.
func runStatus(ctx context.Context, dir string, local bool) error {
	var fetcher *schemas.Fetcher
	if !local {
		fetcher = schemas.NewFetcher()
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, statusTimeout)
		defer cancel()
	}
	rows, err := computeStatus(ctx, dir, fetcher)
	if err != nil {
		return err
	}
	return printStatus(rows)
}

// computeStatus compares each pulled schema against its recorded content and,
// when fetcher is non-nil, against the current remote. A nil fetcher skips the
// network (so 'outdated' is never reported). It is the testable core of status.
func computeStatus(ctx context.Context, dir string, fetcher *schemas.Fetcher) ([]statusRow, error) {
	m, err := schemas.LoadManifest(dir)
	if err != nil {
		return nil, withCode(1, err)
	}
	locals, err := schemas.ListLocal(dir)
	if err != nil {
		return nil, withCode(1, err)
	}

	set := make(map[string]bool)
	for p := range m.Schemas {
		set[p] = true
	}
	for _, p := range locals {
		set[p] = true
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// Read every local file once up front, so the remote fetch can be narrowed
	// to only the paths whose remote bytes are actually compared below.
	type localState struct {
		data   []byte
		exists bool
	}
	reads := make(map[string]localState, len(paths))
	for _, p := range paths {
		data, exists, err := schemas.Read(dir, p)
		if err != nil {
			return nil, withCode(1, err)
		}
		reads[p] = localState{data: data, exists: exists}
	}

	// Only tracked, on-disk schemas reach the remote-comparison branch. Fetching
	// untracked (user-dropped) or missing paths would be a guaranteed 404 whose
	// result is discarded, so leave them out of the fan-out entirely.
	var fetchPaths []string
	for _, p := range paths {
		if _, tracked := m.Schemas[p]; tracked && reads[p].exists {
			fetchPaths = append(fetchPaths, p)
		}
	}

	// Fetch the remaining remotes concurrently rather than in N serial round
	// trips. A per-path error is kept (not discarded) so a failed check reads as
	// "remote unknown" instead of silently passing for "up to date".
	remotes := fetchRemotes(ctx, fetcher, fetchPaths)

	rows := make([]statusRow, 0, len(paths))
	for _, p := range paths {
		entry, tracked := m.Schemas[p]
		ls := reads[p]

		switch {
		case !ls.exists:
			rows = append(rows, statusRow{p, "missing"})
		case !tracked:
			rows = append(rows, statusRow{p, "untracked"})
		default:
			modified := schemas.CanonicalSHA(ls.data) != entry.SHA256
			outdated, remoteUnknown := false, false
			if fetcher != nil {
				switch rr := remotes[p]; {
				case rr.err != nil:
					remoteUnknown = true
				default:
					outdated = schemas.CanonicalSHA(rr.data) != entry.SHA256
				}
			}
			rows = append(rows, statusRow{p, statusLabel(modified, outdated, remoteUnknown)})
		}
	}

	return rows, nil
}

// remoteResult is one schema's fetched bytes or the error that prevented it.
type remoteResult struct {
	data []byte
	err  error
}

// maxRemoteFetches bounds how many schema fetches run at once. A large or
// custom-heavy store would otherwise burst one connection per path at GitHub
// and risk rate-limiting (surfacing as spurious "remote unknown").
const maxRemoteFetches = 8

// fetchRemotes fetches every path from the remote concurrently and returns the
// results keyed by path. A nil fetcher (local-only mode) returns an empty map.
// Concurrency is capped at maxRemoteFetches via a semaphore.
func fetchRemotes(ctx context.Context, fetcher *schemas.Fetcher, paths []string) map[string]remoteResult {
	out := make(map[string]remoteResult, len(paths))
	if fetcher == nil {
		return out
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxRemoteFetches)
	)
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data, err := fetcher.Fetch(ctx, p)
			mu.Lock()
			out[p] = remoteResult{data: data, err: err}
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return out
}

// statusLabel renders the combined modified/outdated state. remoteUnknown marks
// a schema whose remote check failed, so it is never mistaken for up to date.
func statusLabel(modified, outdated, remoteUnknown bool) string {
	switch {
	case modified && remoteUnknown:
		return "modified, remote unknown"
	case remoteUnknown:
		return "remote unknown"
	case modified && outdated:
		return "modified, outdated"
	case modified:
		return "modified"
	case outdated:
		return "outdated"
	default:
		return "up to date"
	}
}

// pullResult is what `schema pull` did, as paths rather than counts so JSON
// consumers can act on the specific schemas rather than re-deriving them.
type pullResult struct {
	Pulled   []string `json:"pulled"`
	UpToDate []string `json:"up_to_date"`
	Kept     []string `json:"kept"`
}

// printPullSummary writes the result: a tally in pretty mode, the object in
// JSON mode. Either way it is the only thing on stdout, because the per-schema
// progress went to stderr.
func printPullSummary(res pullResult) error {
	r := rootApp.renderer
	if jsonMode(r) {
		// Normalise nil to [] so consumers can index without a null check.
		if res.Pulled == nil {
			res.Pulled = []string{}
		}
		if res.UpToDate == nil {
			res.UpToDate = []string{}
		}
		if res.Kept == nil {
			res.Kept = []string{}
		}
		return emitJSON(r, res)
	}
	fmt.Fprintf(r.Out, "\n%d pulled, %d up to date, %d kept\n", len(res.Pulled), len(res.UpToDate), len(res.Kept))
	return nil
}

// printStatus renders status rows. JSON mode emits the rows for piping; pretty
// mode prints a styled "state  path" line per schema.
func printStatus(rows []statusRow) error {
	r := rootApp.renderer
	if r.Mode == ui.ModeJSON {
		raw, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		return r.PrintJSON(raw)
	}
	if len(rows) == 0 {
		fmt.Fprintln(r.Out, "No schemas pulled. Pull one with `tabstack schema pull <name>`.")
		return nil
	}
	for _, row := range rows {
		style := r.Styles.Success
		switch row.State {
		case "missing":
			style = r.Styles.ErrorTag
		case "untracked":
			style = r.Styles.Muted
		default:
			if row.State != "up to date" {
				style = r.Styles.Browser
			}
		}
		fmt.Fprintf(r.Out, "%s  %s\n", style.Render(fmt.Sprintf("%-18s", row.State)), row.Path)
	}
	return nil
}

// printLocalList renders the paths of locally pulled schemas.
func printLocalList(pulled []string) error {
	r := rootApp.renderer
	if r.Mode == ui.ModeJSON {
		// Objects with a "path" key, matching `schema list`, so `jq '.[].path'`
		// works against either. Title and description are absent because
		// --local is offline and only the index carries them.
		out := make([]localEntryJSON, 0, len(pulled))
		for _, rel := range pulled {
			out = append(out, localEntryJSON{Path: rel, Name: baseName(rel)})
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		return r.PrintJSON(raw)
	}
	if len(pulled) == 0 {
		fmt.Fprintln(r.Out, "No schemas pulled. Pull one with `tabstack schema pull <name>`.")
		return nil
	}
	for _, p := range pulled {
		fmt.Fprintln(r.Out, p)
	}
	fmt.Fprintf(r.Out, "\n%d pulled\n", len(pulled))
	return nil
}

// printSchemaList renders the library manifest. JSON mode emits the raw entry
// array for piping; pretty mode groups schemas by category and marks the ones
// already pulled into the local store.
func printSchemaList(idx schemas.Index, pulled map[string]bool) error {
	r := rootApp.renderer
	if r.Mode == ui.ModeJSON {
		raw, err := json.Marshal(idx.Schemas)
		if err != nil {
			return err
		}
		return r.PrintJSON(raw)
	}

	byCat := make(map[string][]schemas.Entry)
	for _, e := range idx.Schemas {
		byCat[e.Category] = append(byCat[e.Category], e)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	for _, c := range cats {
		fmt.Fprintln(r.Out, r.Styles.Label.Render(c))
		entries := byCat[c]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
		for _, e := range entries {
			mark := " "
			if pulled[e.Path] {
				mark = r.Styles.Success.Render("✓")
			}
			fmt.Fprintf(r.Out, "  %s %s  %s\n", mark, e.Path, r.Styles.Muted.Render(e.Title))
		}
	}
	fmt.Fprintf(r.Out, "\n%d schemas, %d pulled\n", len(idx.Schemas), len(pulled))
	return nil
}

// toSet builds a lookup set from a slice of paths.
func toSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set
}

// completePullSelectors offers names, categories, and paths from the library
// index for `schema pull` argument completion.
func completePullSelectors(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	dir, err := storeDirFromCmd(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()
	idx, err := schemas.CachedIndex(ctx, schemas.NewFetcher(), dir, indexCacheTTL, false)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cats := make(map[string]bool)
	var out []string
	for _, e := range idx.Schemas {
		out = append(out, e.Path, baseName(e.Path))
		cats[e.Category] = true
	}
	for c := range cats {
		out = append(out, c)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeLocalSchemaNames offers names and paths of locally pulled schemas, for
// `--schema-name`, `schema rm`, and `schema path` completion.
func completeLocalSchemaNames(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	dir, err := storeDirFromCmd(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	locals, err := schemas.ListLocal(dir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, rel := range locals {
		out = append(out, rel, baseName(rel))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// storeDirFromCmd resolves the store directory during completion, honouring a
// --storage flag if the command defines one.
func storeDirFromCmd(cmd *cobra.Command) (string, error) {
	if cmd != nil && cmd.Flags().Lookup("storage") != nil {
		if s, _ := cmd.Flags().GetString("storage"); s != "" {
			return s, nil
		}
	}
	return schemaStoreDir("")
}

// baseName returns a schema's bare name (no directory, no .json suffix).
func baseName(rel string) string {
	b := path.Base(rel)
	return b[:len(b)-len(path.Ext(b))]
}
