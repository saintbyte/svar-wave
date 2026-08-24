package player

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gopxl/beep/v2/wav"
)

var ErrNotPlaying = errors.New("nothing is playing")

// Supported reports whether the file extension can be played.
func Supported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav", ".mp3", ".flac", ".ogg", ".oga":
		return true
	}
	return false
}

type Status struct {
	Playing    bool   `json:"playing"`
	Paused     bool   `json:"paused"`
	File       string `json:"file,omitempty"`
	PositionMS int64  `json:"position_ms"`
	TotalMS    int64  `json:"total_ms"`
}

// Player plays audio files sequentially through the default speaker,
// one track at a time. It is safe for concurrent use.
type Player struct {
	sampleRate beep.SampleRate
	bufferSize int
	quality    int
	log        *slog.Logger

	initOnce sync.Once
	initErr  error

	mu       sync.Mutex
	ctrl     *beep.Ctrl
	streamer beep.StreamSeekCloser
	format   beep.Format
	file     io.Closer
	done     chan struct{}
	name     string
}

func New(sampleRateHz, bufferMs, resampleQuality int, log *slog.Logger) *Player {
	if log == nil {
		log = slog.Default()
	}
	return &Player{
		sampleRate: beep.SampleRate(sampleRateHz),
		bufferSize: max(256, beep.SampleRate(sampleRateHz).N(time.Duration(bufferMs)*time.Millisecond)),
		quality:    resampleQuality,
		log:        log,
	}
}

func (p *Player) ensureSpeaker() error {
	p.initOnce.Do(func() {
		p.initErr = speaker.Init(p.sampleRate, p.bufferSize)
		if p.initErr != nil {
			p.log.Error("speaker init failed", "err", p.initErr)
		}
	})
	return p.initErr
}

// Play stops the current track (if any) and starts playing path.
func (p *Player) Play(path string) error {
	if err := p.ensureSpeaker(); err != nil {
		return fmt.Errorf("audio device unavailable: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}

	var s beep.StreamSeekCloser
	format := beep.Format{}
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".wav":
		s, format, err = wav.Decode(f)
	case ".mp3":
		s, format, err = mp3.Decode(f)
	case ".flac":
		s, format, err = flac.Decode(f)
	case ".ogg", ".oga":
		s, format, err = vorbis.Decode(f)
	default:
		err = fmt.Errorf("unsupported format %q", ext)
	}
	if err != nil {
		f.Close()
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked(true)

	st := beep.Streamer(s)
	if format.NumChannels == 1 {
		st = monoToStereo{st}
	}
	if format.SampleRate != p.sampleRate {
		st = beep.Resample(p.quality, format.SampleRate, p.sampleRate, st)
	}

	done := make(chan struct{})
	p.ctrl = &beep.Ctrl{Streamer: st, Paused: false}
	p.streamer = s
	p.format = format
	p.file = f
	p.done = done
	p.name = filepath.Base(path)

	speaker.Play(beep.Seq(p.ctrl, beep.Callback(func() { p.onEnded(done) })))
	p.log.Info("playing", "file", p.name, "sample_rate", int(format.SampleRate),
		"channels", format.NumChannels, "duration", format.SampleRate.D(s.Len()).String())
	return nil
}

// onEnded runs on the speaker goroutine when a track finishes naturally.
func (p *Player) onEnded(done chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done != done {
		return // replaced by another track meanwhile; Play already cleaned up
	}
	name := p.name
	p.stopLocked(false)
	p.log.Info("finished", "file", name)
}

// Stop stops playback and releases the current track.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl == nil && p.done == nil {
		return
	}
	name := p.name
	p.stopLocked(true)
	p.log.Info("stopped", "file", name)
}

// TogglePause pauses or resumes playback, returning the new paused state.
func (p *Player) TogglePause() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl == nil {
		return false, ErrNotPlaying
	}
	p.ctrl.Paused = !p.ctrl.Paused
	return p.ctrl.Paused, nil
}

func (p *Player) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{File: p.name}
	if p.ctrl != nil {
		st.Playing = !p.ctrl.Paused
		st.Paused = p.ctrl.Paused
	}
	if p.streamer != nil && p.format.SampleRate > 0 {
		st.PositionMS = p.format.SampleRate.D(p.streamer.Position()).Milliseconds()
		st.TotalMS = p.format.SampleRate.D(p.streamer.Len()).Milliseconds()
	}
	return st
}

// stopLocked must be called with p.mu held.
// If clear is true the global speaker queue is flushed as well.
func (p *Player) stopLocked(clear bool) {
	if clear {
		speaker.Clear()
	}
	p.done = nil
	if p.file != nil {
		_ = p.file.Close()
	}
	p.ctrl = nil
	p.streamer = nil
	p.file = nil
	p.name = ""
	p.format = beep.Format{}
}

type monoToStereo struct{ beep.Streamer }

func (m monoToStereo) Stream(samples [][2]float64) (int, bool) {
	n, ok := m.Streamer.Stream(samples)
	for i := range n {
		l := samples[i][0]
		samples[i][1] = l
	}
	return n, ok
}
