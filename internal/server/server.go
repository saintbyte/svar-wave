package server

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/saintbyte/svar-wave/internal/config"
	"github.com/saintbyte/svar-wave/internal/player"
)

//go:embed web/index.html
var webFS embed.FS

type Server struct {
	cfg    *config.Config
	player *player.Player
	log    *slog.Logger
	app    *fiber.App
}

func New(cfg *config.Config, pl *player.Player, log *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, player: pl, log: log}
	if err := os.MkdirAll(cfg.Storage.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir %s: %w", cfg.Storage.Dir, err)
	}

	app := fiber.New(fiber.Config{
		AppName:   "svar-wave",
		BodyLimit: cfg.HTTP.BodyLimitMB * 1024 * 1024,
	})

	app.Get("/", s.handleIndex)
	app.Get("/healthz", func(c fiber.Ctx) error { return c.SendString("ok") })

	app.Post("/upload", s.handleUpload)
	app.Get("/files", s.handleList)
	app.Post("/play/*", s.handlePlay)
	app.Post("/pause", s.handlePause)
	app.Post("/stop", s.handleStop)
	app.Get("/status", s.handleStatus)

	s.app = app
	return s, nil
}

func (s *Server) Listen() error {
	addr := net.JoinHostPort(s.cfg.HTTP.Host, strconv.Itoa(s.cfg.HTTP.Port))
	s.log.Info("http listening", "addr", addr)
	return s.app.Listen(addr)
}

func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

func (s *Server) handleIndex(c fiber.Ctx) error {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "index.html missing")
	}
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Send(data)
}

func (s *Server) handleUpload(c fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "multipart field \"file\" is required")
	}
	name := sanitizeName(fh.Filename)
	if name == "" || !player.Supported(name) {
		return fiber.NewError(fiber.StatusBadRequest,
			"unsupported file type, allowed: .wav .mp3 .flac .ogg")
	}

	save := true
	switch strings.ToLower(c.Query("save")) {
	case "0", "false", "no", "off":
		save = false
	}

	dest := filepath.Join(s.cfg.Storage.Dir, name)
	if save {
		if err := c.SaveFile(fh, dest); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "save failed: "+err.Error())
		}
	}
	s.log.Info("uploaded", "file", name, "size", fh.Size, "saved", save)

	resp := fiber.Map{"status": "ok", "file": name, "size": fh.Size, "saved": save}

	autoplay := s.cfg.Upload.Autoplay
	switch strings.ToLower(c.Query("autoplay")) {
	case "1", "true", "yes", "on":
		autoplay = true
	case "0", "false", "no", "off":
		autoplay = false
	}
	if autoplay {
		var perr error
		if save {
			perr = s.player.Play(dest)
		} else {
			perr = playFromForm(s.player, name, fh)
		}
		if perr != nil {
			resp["playing"] = false
			resp["error"] = perr.Error()
		} else {
			resp["playing"] = true
		}
	}
	return c.Status(fiber.StatusCreated).JSON(resp)
}

// playFromForm decodes the uploaded multipart file in memory and plays it.
func playFromForm(pl *player.Player, name string, fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("open upload: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read upload: %w", err)
	}
	return pl.PlayBytes(name, data)
}

func (s *Server) handleList(c fiber.Ctx) error {
	dir := s.cfg.Storage.Dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	type item struct {
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		Mtime int64  `json:"mtime_unix"`
	}
	files := make([]item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !player.Supported(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, item{Name: e.Name(), Size: info.Size(), Mtime: info.ModTime().Unix()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Mtime > files[j].Mtime })
	return c.JSON(fiber.Map{"files": files})
}

func (s *Server) handlePlay(c fiber.Ctx) error {
	name := sanitizeName(c.Params("*"))
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "file name required")
	}
	if !player.Supported(name) {
		return fiber.NewError(fiber.StatusBadRequest, "unsupported file type")
	}
	path := filepath.Join(s.cfg.Storage.Dir, name)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return fiber.NewError(fiber.StatusNotFound, "file not found: "+name)
	} else if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if err := s.player.Play(path); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"status": "ok", "playing": name})
}

func (s *Server) handlePause(c fiber.Ctx) error {
	paused, err := s.player.TogglePause()
	if errors.Is(err, player.ErrNotPlaying) {
		return fiber.NewError(fiber.StatusConflict, "nothing is playing")
	}
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"status": "ok", "paused": paused})
}

func (s *Server) handleStop(c fiber.Ctx) error {
	s.player.Stop()
	return c.JSON(fiber.Map{"status": "ok"})
}

func (s *Server) handleStatus(c fiber.Ctx) error {
	return c.JSON(s.player.Status())
}

// sanitizeName strips any path components and dangerous characters.
func sanitizeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(name)
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == ' ', r == '(', r == ')':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
