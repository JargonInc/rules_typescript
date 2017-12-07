package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bazelbuild/rules_typescript/internal/concatjs/concatjs"
	"github.com/bazelbuild/rules_typescript/internal/devserver/devserver"
)

var (
	port             = flag.Int("port", 5432, "server port to listen on")
	base             = flag.String("base", "", "server base (required, runfiles of the binary)")
	pkgs             = flag.String("packages", "", "root package(s) to serve, comma-separated")
	manifest         = flag.String("manifest", "", "sources manifest (.MF)")
	scriptsManifest  = flag.String("scripts_manifest", "", "preScripts manifest (.MF)")
	livereloadScript = flag.String("livereload_script", "", "path to live reload script")
	servingPath      = flag.String("serving_path", "/_/ts_scripts.js", "path to serve the combined sources at")
	entryModule      = flag.String("entry_module", "", "entry module name")
)

func main() {
	flag.Parse()

	if *base == "" || len(*pkgs) == 0 || (*manifest == "") || (*scriptsManifest == "") {
		fmt.Fprintf(os.Stderr, "Required argument not set\n")
		os.Exit(1)
	}

	if _, err := os.Stat(*base); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot read server base %s: %v\n", *base, err)
		os.Exit(1)
	}

	scriptsManifestPath := filepath.Join(*base, *scriptsManifest)
	scriptFiles, err := manifestFiles(scriptsManifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read scripts_manifest: %v\n", err)
		os.Exit(1)
	}

	preScripts := make([]string, 0, 100)
	postScripts := make([]string, 0, 1)

	// Include the livereload script if IBAZEL_LIVERELOAD_URL is set and
	// valid and the script is available. Also output a snippet before the
	// livereload script that sets window.LiveReloadOptions which configures
	// the endpoint livereload will use
	livereloadUrl := os.Getenv("IBAZEL_LIVERELOAD_URL")
	re := regexp.MustCompile("^([a-zA-Z0-9]+)\\:\\/\\/([[a-zA-Z0-9\\.]+)\\:([0-9]+)")
	match := re.FindStringSubmatch(livereloadUrl)
	if match != nil && len(match) == 4 {
		port, err := strconv.ParseUint(match[3], 10, 16)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot determine livereload port from IBAZEL_LIVERELOAD_URL")
		} else {
			livereloadScheme := match[1]
			livereloadHost := match[2]
			livereloadPort := uint16(port)
			if *livereloadScript != "" {
				livereloadJs, err := loadScript(filepath.Join(*base, *livereloadScript))
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to read livereload script: %v\n", err)
				} else {
					preScripts = append(preScripts, fmt.Sprintf("window.LiveReloadOptions = { https: \"%s\" === \"https\", host: \"%s\", port: %d };", livereloadScheme, livereloadHost, livereloadPort))
					preScripts = append(preScripts, livereloadJs)
					fmt.Printf("Serving livereload script for port %s://%s:%d\n", livereloadScheme, livereloadHost, livereloadPort)
				}
			} else {
				fmt.Fprintf(os.Stderr, "livereload script not available\n")
			}
		}
	}

	// Include all user scripts in preScripts. This should always include
	// the requirejs script which is added to scriptFiles by the devserver
	// skylark rule.
	for _, v := range scriptFiles {
		js, err := loadScript(filepath.Join(*base, v))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read script %s: %v\n", v, err)
		} else {
			preScripts = append(preScripts, js)
		}
	}

	// If the entryModule is set then add a snippet to load
	// the application to postScripts to be outputted after the sources
	if *entryModule != "" {
		postScripts = append(postScripts, fmt.Sprintf("require([\"%s\"]);", *entryModule))
	}

	http.Handle(*servingPath, concatjs.ServeConcatenatedJS(*manifest, *base, preScripts, postScripts, nil /* realFileSystem */))
	pkgList := strings.Split(*pkgs, ",")
	http.HandleFunc("/", devserver.CreateFileHandler(*servingPath, *manifest, pkgList, *base))

	h, err := os.Hostname()
	if err != nil {
		h = "localhost"
	}

	fmt.Printf("Server listening on http://%s:%d/\n", h, *port)
	fmt.Fprintln(os.Stderr, http.ListenAndServe(fmt.Sprintf(":%d", *port), nil).Error())
	os.Exit(1)
}

func loadScript(path string) (string, error) {
	buf, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// manifestFiles parses a manifest, returning a list of the files in the manifest.
func manifestFiles(manifest string) ([]string, error) {
	f, err := os.Open(manifest)
	if err != nil {
		return nil, fmt.Errorf("could not read manifest %s: %s", manifest, err)
	}
	defer f.Close()
	return manifestFilesFromReader(f)
}

// manifestFilesFromReader is a helper for manifestFiles, split out for testing.
func manifestFilesFromReader(r io.Reader) ([]string, error) {
	var lines []string
	s := bufio.NewScanner(r)
	for s.Scan() {
		path := s.Text()
		if path == "" {
			continue
		}
		lines = append(lines, path)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}