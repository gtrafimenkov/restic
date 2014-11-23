package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fd0/khepri"
	"github.com/fd0/khepri/backend"
	"golang.org/x/crypto/ssh/terminal"
)

func format_bytes(c uint64) string {
	b := float64(c)

	switch {
	case c > 1<<40:
		return fmt.Sprintf("%.3fTiB", b/(1<<40))
	case c > 1<<30:
		return fmt.Sprintf("%.3fGiB", b/(1<<30))
	case c > 1<<20:
		return fmt.Sprintf("%.3fMiB", b/(1<<20))
	case c > 1<<10:
		return fmt.Sprintf("%.3fKiB", b/(1<<10))
	default:
		return fmt.Sprintf("%dB", c)
	}
}

func format_duration(sec uint64) string {
	hours := sec / 3600
	sec -= hours * 3600
	min := sec / 60
	sec -= min * 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, min, sec)
	}

	return fmt.Sprintf("%d:%02d", min, sec)
}

func print_tree2(indent int, t *khepri.Tree) {
	for _, node := range *t {
		if node.Tree != nil {
			fmt.Printf("%s%s/\n", strings.Repeat("  ", indent), node.Name)
			print_tree2(indent+1, node.Tree)
		} else {
			fmt.Printf("%s%s\n", strings.Repeat("  ", indent), node.Name)
		}
	}
}

func commandBackup(be backend.Server, key *khepri.Key, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: backup [dir|file]")
	}

	target := args[0]

	arch, err := khepri.NewArchiver(be, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
	}
	arch.Error = func(dir string, fi os.FileInfo, err error) error {
		// TODO: make ignoring errors configurable
		fmt.Fprintf(os.Stderr, "\nerror for %s: %v\n%v\n", dir, err, fi)
		return nil
	}

	fmt.Printf("scanning %s\n", target)

	if terminal.IsTerminal(int(os.Stdout.Fd())) {
		ch := make(chan khepri.Stats, 20)
		arch.ScannerStats = ch

		go func(ch <-chan khepri.Stats) {
			for stats := range ch {
				fmt.Printf("\r%6d directories, %6d files, %14s", stats.Directories, stats.Files, format_bytes(stats.Bytes))
			}
		}(ch)
	}

	fmt.Printf("done\n")

	// TODO: add filter
	// arch.Filter = func(dir string, fi os.FileInfo) bool {
	// 	return true
	// }

	t, err := arch.LoadTree(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return err
	}

	fmt.Printf("\r%6d directories, %6d files, %14s\n", arch.Stats.Directories, arch.Stats.Files, format_bytes(arch.Stats.Bytes))

	stats := khepri.Stats{}
	start := time.Now()
	if terminal.IsTerminal(int(os.Stdout.Fd())) {
		ch := make(chan khepri.Stats, 20)
		arch.SaveStats = ch

		ticker := time.NewTicker(time.Second)
		var eta, bps uint64

		go func(ch <-chan khepri.Stats) {

			status := func(d time.Duration) {
				fmt.Printf("\x1b[2K\r[%s] %3.2f%%  %s/s  %s / %s  ETA %s",
					format_duration(uint64(d/time.Second)),
					float64(stats.Bytes)/float64(arch.Stats.Bytes)*100,
					format_bytes(bps),
					format_bytes(stats.Bytes), format_bytes(arch.Stats.Bytes),
					format_duration(eta))
			}

			defer ticker.Stop()
			for {
				select {
				case s, ok := <-ch:
					if !ok {
						return
					}
					stats.Files += s.Files
					stats.Directories += s.Directories
					stats.Other += s.Other
					stats.Bytes += s.Bytes

					status(time.Since(start))
				case <-ticker.C:
					d := time.Since(start)
					bps = stats.Bytes * uint64(time.Second) / uint64(d)

					if bps > 0 {
						eta = (arch.Stats.Bytes - stats.Bytes) / bps
					}

					status(d)
				}
			}
		}(ch)
	}

	sn, id, err := arch.Snapshot(target, t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}

	if terminal.IsTerminal(int(os.Stdout.Fd())) {
		// close channels so that the goroutines terminate
		close(arch.SaveStats)
		close(arch.ScannerStats)
	}

	fmt.Printf("\nsnapshot %s saved: %v\n", id, sn)
	duration := time.Now().Sub(start)
	fmt.Printf("duration: %s, %.2fMiB/s\n", duration, float64(arch.Stats.Bytes)/float64(duration/time.Second)/(1<<20))

	return nil
}
