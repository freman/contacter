package main

import (
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/labstack/echo/v4"
)

// TemplateRegistry implements echo.Renderer to cache templates in memory
type TemplateRegistry struct {
	templates map[string]*template.Template
	mu        sync.RWMutex
}

func NewTemplateRegistry(templatesDir string) (*TemplateRegistry, error) {
	tr := &TemplateRegistry{
		templates: make(map[string]*template.Template),
	}

	// Initial load of templates
	if err := tr.loadTemplates(templatesDir); err != nil {
		return nil, err
	}

	// Start watching for file changes
	go tr.watchTemplates(templatesDir)

	return tr, nil
}

func (tr *TemplateRegistry) loadTemplates(templatesDir string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	// Clear existing templates
	tr.templates = make(map[string]*template.Template)

	// Walk through templates directory
	err := filepath.Walk(templatesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".html" {
			// Parse template
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
		fmt.Printf("Failed to create watcher: %v\n", err)
		return
	}
	defer watcher.Close()

	// Watch the templates directory and subdirectories
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
		fmt.Printf("Failed to watch templates directory: %v\n", err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
				fmt.Printf("Template changed: %s, reloading templates\n", event.Name)
				if err := tr.loadTemplates(templatesDir); err != nil {
					fmt.Printf("Failed to reload templates: %v\n", err)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("Watcher error: %v\n", err)
		}
	}
}

func (tr *TemplateRegistry) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	// Use the provided name (domain) to construct the template path
	tmplPath := filepath.Join(strings.ToLower(name), "contact.html")
	// Check if domain-specific template exists
	tmpl, ok := tr.templates[tmplPath]
	if !ok {
		// Fall back to default template
		tmplPath = filepath.Join("default", "contact.html")
		tmpl, ok = tr.templates[tmplPath]
		if !ok {
			return fmt.Errorf("template %s not found (fallback to default also failed)", tmplPath)
		}
	}
	return tmpl.Execute(w, data)

}
