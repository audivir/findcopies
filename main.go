package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/gobwas/glob"
	"github.com/urfave/cli/v3"
)

// version is set with -ldflags -X main.version=... for release builds, resolved from the
// module build info for go install, and falls back to dev otherwise.
var version = "dev"

func init() {
	if version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
}

func main() {
	app := &cli.Command{
		Name:    "findcopies",
		Usage:   "Find files matching a query file's sha256 hash.",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "query",
				Aliases:  []string{"q"},
				Usage:    "Path to the query file [required]",
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:    "include",
				Aliases: []string{"i"},
				Usage:   "Glob patterns to include (default all)",
			},
			&cli.StringSliceFlag{
				Name:    "exclude",
				Aliases: []string{"e"},
				Usage:   "Glob patterns to exclude",
			},
			&cli.IntFlag{
				Name:    "workers",
				Aliases: []string{"w", "j"},
				Usage:   "Number of parallel workers",
				Value:   runtime.NumCPU(),
			},
			// &cli.IntFlag{
			// 	Name:    "depth",
			// 	Aliases: []string{"d"},
			// 	Usage:   "Descend only depth directories deep.",
			// },
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Print errors and skipped files to stderr",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			queryFile := cmd.String("query")
			includes := cmd.StringSlice("include")
			excludes := cmd.StringSlice("exclude")
			dirs := cmd.Args().Slice()
			if len(dirs) == 0 {
				dirs = []string{"."}
			}
			workers := cmd.Int("workers")
			// depth := cmd.Int("depth")
			verbose := cmd.Bool("verbose")

			querySize, queryMD5, querySHA, err := processQueryFile(queryFile)
			if err != nil {
				return err
			}
			includeGlobs, excludeGlobs, err := processGlobs(includes, excludes)
			if err != nil {
				return err
			}

			if verbose {
				msg := fmt.Sprintf("Searching for copies of %s in %v", queryFile, dirs)

				if len(includes) > 0 {
					msg += fmt.Sprintf(" with files matching %v", includes)
				}
				if len(excludes) > 0 {
					if len(includes) > 0 {
						msg += fmt.Sprintf(" but not %v", excludes)
					} else {
						msg += fmt.Sprintf(" with files not matching %v", excludes)
					}
				}

				fmt.Fprintln(os.Stderr, msg)
			}

			// Channels for work + results
			fileCh := make(chan string, 100)
			var wg sync.WaitGroup

			// Start workers
			for range workers {
				wg.Go(func() {
					for path := range fileCh {
						processFile(path, querySize, queryMD5, querySHA, verbose)
					}
				})
			}

			// Walk directories and send files to workers
			for _, dir := range dirs {
				walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						if verbose {
							fmt.Fprintf(os.Stderr, "walk error: %s: %v\n", path, err)
						}
						return nil
					}

					if d.IsDir() {
						return nil
					}

					rel := filepath.Base(path)

					// include check
					if len(includeGlobs) > 0 {
						matched := false
						for _, g := range includeGlobs {
							if g.Match(rel) {
								matched = true
								break
							}
						}
						if !matched {
							return nil
						}
					}

					// exclude check
					for _, g := range excludeGlobs {
						if g.Match(rel) {
							return nil
						}
					}

					fileCh <- path
					return nil
				})
				if walkErr != nil {
					fmt.Fprintf(os.Stderr, "walk error: %s: %v\n", dir, walkErr)
				}
			}

			close(fileCh)
			wg.Wait()
			return nil
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func hashFirstKB(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := md5.New()
	buf := make([]byte, 1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	h.Write(buf[:n])
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func processGlobs(includes []string, excludes []string) ([]glob.Glob, []glob.Glob, error) {
	// Compile include/exclude globs
	var includeGlobs []glob.Glob
	for _, p := range includes {
		g, err := glob.Compile(p)
		if err != nil {
			return nil, nil, fmt.Errorf("bad include pattern %q: %w", p, err)
		}
		includeGlobs = append(includeGlobs, g)
	}
	var excludeGlobs []glob.Glob
	for _, p := range excludes {
		g, err := glob.Compile(p)
		if err != nil {
			return nil, nil, fmt.Errorf("bad exclude pattern %q: %w", p, err)
		}
		excludeGlobs = append(excludeGlobs, g)
	}
	return includeGlobs, excludeGlobs, nil
}
func processQueryFile(queryFile string) (int64, string, string, error) {
	// query file size + hashes
	queryInfo, err := os.Stat(queryFile)
	if err != nil {
		return 0, "", "", fmt.Errorf("cannot stat query file: %w", err)
	}
	querySize := queryInfo.Size()

	queryMD5, err := hashFirstKB(queryFile)
	if err != nil {
		return 0, "", "", fmt.Errorf("query file md5 error: %w", err)
	}
	querySHA, err := hashSHA256(queryFile)
	if err != nil {
		return 0, "", "", fmt.Errorf("query file sha256 error: %w", err)
	}

	return querySize, queryMD5, querySHA, nil
}
func processFile(path string, querySize int64, queryMD5 string, querySHA string, verbose bool) {
	// size check first
	info, err := os.Stat(path)
	if err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "stat error: %s: %v\n", path, err)
		}
		return
	}
	if info.Size() != querySize {
		return
	}

	// first 1KB MD5
	md5sum, err := hashFirstKB(path)
	if err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "md5 error: %s: %v\n", path, err)
		}
		return
	}
	if md5sum != queryMD5 {
		return
	}

	// full SHA256
	sha, err := hashSHA256(path)
	if err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "sha256 error: %s: %v\n", path, err)
		}
		return
	}
	if sha == querySHA {
		fmt.Println(path)
	}
}
