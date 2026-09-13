package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
	"github.com/spf13/cobra"
)

// Speed test configuration: how many parallel streams and the adaptive
// per-stream payload sizes, so the test scales up to multi-gigabit links.
const (
	cfBase       = "https://speed.cloudflare.com"
	cfPingCount  = 5
	cfStreams    = 4
	cfMinSample  = 250 * time.Millisecond
	chestPrimary = "\x1b[38;5;114m" // green
	chestGold    = "\x1b[38;5;220m"
	chestCyan    = "\x1b[38;5;75m"
	chestRed     = "\x1b[38;5;196m"
	chestDim     = "\x1b[38;5;246m"
	chestReset   = "\x1b[0m"
)

var (
	cfDownSizes = []int64{1 << 20, 4 << 20, 16 << 20, 64 << 20, 256 << 20}
	cfUpSizes   = []int64{1 << 20, 4 << 20, 16 << 20, 64 << 20}
)

// speedResult holds one measurement for the comparison table.
type speedResult struct {
	label  string
	ok     bool
	detail string
	err    string
}

// newSpeedtestCmd returns the `chest speedtest` command. It runs two
// independent tests (Ookla + Cloudflare) side-by-side so you can compare,
// and can optionally run a local disk read/write benchmark via --disk.
func newSpeedtestCmd() *cobra.Command {
	var onlyOokla, onlyCloudflare bool
	var jsonOut bool
	var disk bool
	var diskPath string
	var diskSizeMiB int64

	cmd := &cobra.Command{
		Use:     "speedtest",
		Aliases: []string{"net", "network"},
		Short:   "Measure network speed (download/upload/ping/jitter) via Ookla + Cloudflare, or disk I/O with --disk",
		Long: `Measure your connection speed - and, with --disk, your local storage - from the terminal, no API key required.

NETWORK - runs TWO independent tests side-by-side in a comparison table:
  • Ookla      - the real speedtest.net protocol: nearest-server selection,
                 multi-stream download/upload, latency/jitter. Most accurate.
  • Cloudflare - a stdlib-only test against speed.cloudflare.com.

DISK - add --disk to benchmark local block I/O on a private temp file
(sequential MB/s read/write + random 4K IOPS). Writes are flushed so they
reflect durable speed; reads may be served from the OS page cache.

Use --ookla or --cloudflare to run only one network test. Use --json for
machine-readable output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rows := make([]speedResult, 0, 6)

			switch {
			case onlyCloudflare:
				runCloudflare(cmd.ErrOrStderr(), &rows)
				if disk {
					runDisk(cmd.ErrOrStderr(), &rows, diskPath, diskSizeMiB)
				}
			case onlyOokla:
				runOokla(cmd.ErrOrStderr(), &rows)
				if disk {
					runDisk(cmd.ErrOrStderr(), &rows, diskPath, diskSizeMiB)
				}
			case disk:
				runDisk(cmd.ErrOrStderr(), &rows, diskPath, diskSizeMiB)
			default:
				runOokla(cmd.ErrOrStderr(), &rows)
				runCloudflare(cmd.ErrOrStderr(), &rows)
			}

			if jsonOut {
				printSpeedJSON(cmd.OutOrStdout(), rows)
			} else {
				printSpeedTable(cmd.OutOrStdout(), rows)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&onlyOokla, "ookla", false, "Run only the Ookla (speedtest.net) test")
	cmd.Flags().BoolVar(&onlyCloudflare, "cloudflare", false, "Run only the Cloudflare test")
	cmd.Flags().BoolVar(&disk, "disk", false, "Run a local disk read/write benchmark (sequential MB/s + random 4K IOPS)")
	cmd.Flags().StringVar(&diskPath, "disk-path", "", "Directory for the disk benchmark (default: system temp)")
	cmd.Flags().Int64Var(&diskSizeMiB, "disk-size", defaultDiskSizeMiB, fmt.Sprintf("Disk benchmark test-file size in MiB (max %d)", maxDiskSizeMiB))
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output results as JSON")
	return cmd
}

// runOokla performs the accurate speedtest.net protocol test against the
// nearest available Ookla server, showing live progress on errOut.
func runOokla(errOut io.Writer, rows *[]speedResult) {
	printSpin(errOut, "Locating nearest Ookla server...")
	sclient := speedtest.New()
	servers, err := sclient.FetchServers()
	if err != nil {
		clearSpin(errOut)
		*rows = append(*rows, speedResult{label: "Ookla (server)", ok: false, err: fmt.Sprintf("fetch servers: %v", err)})
		return
	}
	srv, err := servers.FindServer([]int{})
	clearSpin(errOut)
	if err != nil || len(srv) == 0 {
		*rows = append(*rows, speedResult{label: "Ookla (server)", ok: false, err: errFetch})
		return
	}
	s := srv[0]

	fmt.Fprintf(errOut, "  %sOokla server:%s %s (%s%.0f km%s)\n",
		chestCyan, chestReset, s.Name, chestGold, s.Distance, chestReset)

	// Ping + jitter
	printSpin(errOut, "Pinging "+s.Name+"...")
	lastLat := time.Duration(0)
	pingErr := s.PingTest(func(lat time.Duration) { lastLat = lat })
	clearSpin(errOut)
	if pingErr != nil {
		*rows = append(*rows, speedResult{label: "Ookla ping", ok: false, err: pingErr.Error()})
	} else {
		jit := s.Jitter.Milliseconds()
		fmt.Fprintf(errOut, "  %sPing:%s %d ms  %s- jitter %d ms%s\n",
			chestPrimary, chestReset, lastLat.Milliseconds(), chestDim, jit, chestReset)
		*rows = append(*rows, speedResult{label: "Ookla ping", ok: true,
			detail: fmt.Sprintf("%d ms (jitter %d ms)", lastLat.Milliseconds(), jit)})
	}

	// Download (multi-stream)
	printSpin(errOut, "Testing download over multiple streams...")
	if err := s.DownloadTest(); err != nil {
		clearSpin(errOut)
		*rows = append(*rows, speedResult{label: "Ookla download", ok: false, err: err.Error()})
	} else {
		clearSpin(errOut)
		fmt.Fprintf(errOut, "  %sDownload:%s %6.2f Mbps\n", chestPrimary, chestReset, s.DLSpeed.Mbps())
		*rows = append(*rows, speedResult{label: "Ookla download", ok: true, detail: fmt.Sprintf("%.2f Mbps", s.DLSpeed.Mbps())})
	}

	// Upload (multi-stream)
	printSpin(errOut, "Testing upload over multiple streams...")
	if err := s.UploadTest(); err != nil {
		clearSpin(errOut)
		*rows = append(*rows, speedResult{label: "Ookla upload", ok: false, err: err.Error()})
	} else {
		clearSpin(errOut)
		fmt.Fprintf(errOut, "  %sUpload:%s   %6.2f Mbps\n", chestPrimary, chestReset, s.ULSpeed.Mbps())
		*rows = append(*rows, speedResult{label: "Ookla upload", ok: true, detail: fmt.Sprintf("%.2f Mbps", s.ULSpeed.Mbps())})
	}

	s.Context.Reset()
}

var errFetch = "no server available or found"

// runCloudflare performs the self-contained stdlib-only test against
// Cloudflare, showing live progress on errOut.
func runCloudflare(errOut io.Writer, rows *[]speedResult) {
	client := &http.Client{Timeout: 2 * time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var lat, jit time.Duration
	var ok bool

	printSpin(errOut, "Pinging Cloudflare...")
	lat, jit, _ = cfPing(ctx, client)
	clearSpin(errOut)
	if lat > 0 {
		fmt.Fprintf(errOut, "  %sPing:%s %d ms  %s- jitter %d ms%s\n",
			chestPrimary, chestReset, lat.Milliseconds(), chestDim, jit.Milliseconds(), chestReset)
		*rows = append(*rows, speedResult{label: "Cloudflare ping", ok: true,
			detail: fmt.Sprintf("%d ms (jitter %d ms)", lat.Milliseconds(), jit.Milliseconds())})
		ok = true
	}

	printSpin(errOut, "Testing download (4 streams)...")
	dl, dErr := cfDownload(ctx, client)
	clearSpin(errOut)
	if dErr != nil {
		*rows = append(*rows, speedResult{label: "Cloudflare download", ok: false, err: dErr.Error()})
	} else {
		fmt.Fprintf(errOut, "  %sDownload:%s %6.2f Mbps\n", chestPrimary, chestReset, dl)
		*rows = append(*rows, speedResult{label: "Cloudflare download", ok: true, detail: fmt.Sprintf("%.2f Mbps", dl)})
	}

	printSpin(errOut, "Testing upload (4 streams)...")
	ul, uErr := cfUpload(ctx, client)
	clearSpin(errOut)
	if uErr != nil {
		*rows = append(*rows, speedResult{label: "Cloudflare upload", ok: false, err: uErr.Error()})
	} else {
		fmt.Fprintf(errOut, "  %sUpload:%s   %6.2f Mbps\n", chestPrimary, chestReset, ul)
		*rows = append(*rows, speedResult{label: "Cloudflare upload", ok: true, detail: fmt.Sprintf("%.2f Mbps", ul)})
	}

	if !ok && dErr != nil && uErr != nil {
		*rows = append(*rows, speedResult{label: "Cloudflare", ok: false, err: "unreachable"})
	}
}

func cfPing(ctx context.Context, client *http.Client) (best, jitter time.Duration, err error) {
	if _, err = cfPingOnce(ctx, client); err != nil { // warm-up, discarded
		return 0, 0, err
	}
	var samples []time.Duration
	for i := 0; i < cfPingCount; i++ {
		s, e := cfPingOnce(ctx, client)
		if e != nil {
			return 0, 0, e
		}
		samples = append(samples, s)
	}
	best = samples[0]
	var sum time.Duration
	for _, s := range samples {
		sum += s
		if s < best {
			best = s
		}
	}
	mean := sum / time.Duration(len(samples))
	var dev time.Duration
	for _, s := range samples {
		d := s - mean
		if d < 0 {
			d = -d
		}
		dev += d
	}
	return best, dev / time.Duration(len(samples)), nil
}

func cfPingOnce(ctx context.Context, client *http.Client) (time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/__down?bytes=0", cfBase), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("ping: status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return time.Since(start), nil
}

// cfDownload runs parallel streams and grows the payload adaptively so that
// fast links get a multi-second sustained measurement instead of a blip.
func cfDownload(ctx context.Context, client *http.Client) (float64, error) {
	var best float64
	for phase, size := range cfDownSizes {
		start := time.Now()
		total, err := cfStream(ctx, client, size, false, 0)
		if err != nil {
			if phase == 0 {
				return 0, err
			}
			break
		}
		secs := time.Since(start).Seconds()
		if secs >= cfMinSample.Seconds() {
			if mb := mbps(total, secs); mb > best {
				best = mb
			}
		}
		if secs >= 3 {
			break
		}
	}
	return best, nil
}

func cfUpload(ctx context.Context, client *http.Client) (float64, error) {
	var best float64
	for phase, size := range cfUpSizes {
		start := time.Now()
		total, err := cfStream(ctx, client, size, true, realBytes(size))
		if err != nil {
			if phase == 0 {
				return 0, err
			}
			break
		}
		secs := time.Since(start).Seconds()
		if secs >= cfMinSample.Seconds() {
			if mb := mbps(total, secs); mb > best {
				best = mb
			}
		}
		if secs >= 3 {
			break
		}
	}
	return best, nil
}

// cfStream runs cfStreams parallel requests and sums the transferred bytes.
func cfStream(ctx context.Context, client *http.Client, perStream int64, upload bool, n int64) (int64, error) {
	type r struct {
		n   int64
		err error
	}
	ch := make(chan r, cfStreams)
	for i := 0; i < cfStreams; i++ {
		go func() {
			if upload {
				respBytes, err := cfUploadOnce(ctx, client, perStream)
				ch <- r{respBytes, err}
			} else {
				got, err := cfDownloadOnce(ctx, client, perStream)
				ch <- r{got, err}
			}
		}()
	}
	var total int64
	for i := 0; i < cfStreams; i++ {
		rr := <-ch
		if rr.err != nil {
			return total, rr.err
		}
		total += rr.n
	}
	return total, nil
}

func cfDownloadOnce(ctx context.Context, client *http.Client, size int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/__down?bytes=%d", cfBase, size), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("download: status %d", resp.StatusCode)
	}
	return io.Copy(io.Discard, resp.Body)
}

func cfUploadOnce(ctx context.Context, client *http.Client, size int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfBase+"/__up", &zeroReader{n: size})
	if err != nil {
		return 0, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("upload: status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return realBytes(size), nil
}
