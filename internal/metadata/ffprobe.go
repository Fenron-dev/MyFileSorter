package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dennis/myfilesorter/internal/domain"
)

const (
	ffprobeTimeout        = 20 * time.Second
	ffprobeMaxStdoutBytes = 2 << 20
	ffprobeMaxStderrBytes = 64 << 10
	ffprobeMaxTags        = 256
	ffprobeMaxTagRunes    = 1000
)

type FFProbeReader struct {
	path string
}

func NewFFProbeReader() Reader {
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return NoopReader{}
	}
	return FFProbeReader{path: path}
}

func (r FFProbeReader) Available() bool { return r.path != "" }

func (r FFProbeReader) Read(ctx context.Context, path string) (domain.EmbeddedMetadata, error) {
	fileContext, cancel := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancel()

	stdout := newCappedBuffer(ffprobeMaxStdoutBytes)
	stderr := newCappedBuffer(ffprobeMaxStderrBytes)
	cmd := exec.CommandContext(fileContext, r.path, ffprobeArguments(path)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if errors.Is(fileContext.Err(), context.DeadlineExceeded) {
		return domain.EmbeddedMetadata{}, fmt.Errorf("ffprobe-Zeitlimit von %s überschritten", ffprobeTimeout)
	}
	if stdout.Truncated() {
		return domain.EmbeddedMetadata{}, fmt.Errorf("ffprobe-Ausgabe überschreitet %d Bytes", ffprobeMaxStdoutBytes)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if stderr.Truncated() {
			detail += " … (gekürzt)"
		}
		if detail != "" {
			return domain.EmbeddedMetadata{}, fmt.Errorf("ffprobe: %w: %s", err, detail)
		}
		return domain.EmbeddedMetadata{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseFFProbe(stdout.Bytes())
}

func ffprobeArguments(path string) []string {
	return []string{
		"-nostdin",
		"-hide_banner",
		"-v", "error",
		"-protocol_whitelist", "file,crypto,data",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-i", path,
	}
}

type cappedBuffer struct {
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func newCappedBuffer(maximum int) *cappedBuffer {
	return &cappedBuffer{maximum: maximum}
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.maximum - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || originalLength > 0
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *cappedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *cappedBuffer) String() string { return b.buffer.String() }

func (b *cappedBuffer) Truncated() bool { return b.truncated }

type ffprobeDocument struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType string            `json:"codec_type"`
		Duration  string            `json:"duration"`
		Tags      map[string]string `json:"tags"`
	} `json:"streams"`
}

func parseFFProbe(data []byte) (domain.EmbeddedMetadata, error) {
	var doc ffprobeDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return domain.EmbeddedMetadata{}, fmt.Errorf("parse ffprobe output: %w", err)
	}

	tags := normalizeTags(doc.Format.Tags)
	duration := doc.Format.Duration
	for _, stream := range doc.Streams {
		if stream.CodecType != "audio" {
			continue
		}
		for key, value := range normalizeTags(stream.Tags) {
			if _, exists := tags[key]; !exists {
				tags[key] = value
			}
		}
		if duration == "" {
			duration = stream.Duration
		}
		break
	}

	return domain.EmbeddedMetadata{
		Title:          firstTag(tags, "title"),
		Album:          firstTag(tags, "album"),
		Artist:         firstTag(tags, "artist", "author"),
		AlbumArtist:    firstTag(tags, "album_artist", "albumartist"),
		Series:         firstTag(tags, "series", "mvnm"),
		SeriesSequence: firstTag(tags, "series-part", "series_part", "seriespart", "mvin"),
		Narrator:       firstTag(tags, "narrator", "composer"),
		Language:       firstTag(tags, "language", "lang"),
		ASIN:           firstTag(tags, "asin", "audible_asin", "audible asin"),
		ISBN:           firstTag(tags, "isbn"),
		Track:          leadingInt(firstTag(tags, "track", "tracknumber")),
		Disc:           boundedLeadingInt(firstTag(tags, "disc", "discnumber", "disk"), 9999),
		DurationMillis: durationMillis(duration),
	}, nil
}

func normalizeTags(input map[string]string) map[string]string {
	capacity := min(len(input), ffprobeMaxTags)
	out := make(map[string]string, capacity)
	for key, value := range input {
		if len(out) >= ffprobeMaxTags {
			break
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" || len(key) > 256 {
			continue
		}
		value = limitRunes(strings.TrimSpace(strings.ToValidUTF8(value, "")), ffprobeMaxTagRunes)
		out[key] = value
		switch {
		case strings.HasSuffix(key, ":asin"), strings.HasSuffix(key, ".asin"):
			out["asin"] = value
		case strings.HasSuffix(key, ":isbn"), strings.HasSuffix(key, ".isbn"):
			out["isbn"] = value
		case strings.HasSuffix(key, ":series-part"), strings.HasSuffix(key, ":series_part"):
			out["series-part"] = value
		case strings.HasSuffix(key, ":series"):
			out["series"] = value
		}
	}
	return out
}

func firstTag(tags map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(tags[key]); value != "" {
			return value
		}
	}
	return ""
}

func leadingInt(value string) int {
	return boundedLeadingInt(value, 999999)
}

func boundedLeadingInt(value string, maximum int) int {
	value = strings.TrimSpace(value)
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	n, _ := strconv.Atoi(value)
	if n < 0 || n > maximum {
		return 0
	}
	return n
}

func durationMillis(value string) int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	const maximumSeconds = 10 * 365 * 24 * 60 * 60
	if err != nil || seconds <= 0 || seconds > maximumSeconds {
		return 0
	}
	return int64(seconds * 1000)
}

func limitRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}
