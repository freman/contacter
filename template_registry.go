package main

import (
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/labstack/echo/v5"
)

type TemplateRegistry struct {
	templates map[string]*template.Template
	mu        sync.RWMutex
}

func NewTemplateRegistry(templatesDir string) (*TemplateRegistry, error) {
	tr := &TemplateRegistry{
		templates: make(map[string]*template.Template),
	}

	if err := tr.loadTemplates(templatesDir); err != nil {
		return nil, err
	}

	go tr.watchTemplates(templatesDir)

	return tr, nil
}

func (tr *TemplateRegistry) loadTemplates(templatesDir string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	tr.templates = make(map[string]*template.Template)

	err := filepath.Walk(templatesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Ext(path) == ".html" {
			tmpl, err := template.ParseFiles(path)
			if err != nil {
				return fmt.Errorf("failed to parse template %s: %v", path, err)
			}

			relPath, _ := filepath.Rel(templatesDir, path)
			tr.templates[relPath] = tmpl
		}

		return nil
	})

	return err
}

func (tr *TemplateRegistry) watchTemplates(templatesDir string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("failed to create template watcher", "err", err)

		return
	}
	defer watcher.Close()

	err = filepath.Walk(templatesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return watcher.Add(path)
		}

		return nil
	})
	if err != nil {
		slog.Error("failed to watch templates directory", "err", err)

		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
				slog.Info("template changed, reloading", "file", event.Name)

				if err := tr.loadTemplates(templatesDir); err != nil {
					slog.Error("failed to reload templates", "err", err)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}

			slog.Error("template watcher error", "err", err)
		}
	}
}

func (tr *TemplateRegistry) Render(c *echo.Context, w io.Writer, name string, data any) error {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	tmplPath := filepath.Join(strings.ToLower(name), "contact.html")
	tmpl, ok := tr.templates[tmplPath]
	if !ok {
		tmplPath = filepath.Join("default", "contact.html")
		tmpl, ok = tr.templates[tmplPath]
		if !ok {
			return fmt.Errorf("template %s not found (fallback to default also failed)", tmplPath)
		}
	}

	return tmpl.Execute(w, data)
}
