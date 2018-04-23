package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/borislav-rangelov/bpm/commands"
)

const dependencyFilename = "bpm.json"
const vendorFolderName = "vendor"
const gitFolderName = ".git"

func main() {

	var (
		c   = &commands.Commands{}
		dir = ""
	)
	c.Name = "Basic Package Manager"
	c.MainCommand = "bpm"
	c.NewCommand("init", func() {
		doInit(getCurrentDir())
	}, "Creates a bpm.json file in the current directory and gets all dependencies.")
	c.NewCommand("install", func() {
		doInstall(getDir(&dir))
	}, "Pulls configured packages and version.")
	c.NewCommand("rebuild", func() {
		doRebuild(getDir(&dir))
	}, "Forgets all dependency data and pulls latest package versions.")
	c.NewArg("dir", &dir, dir, "Root dir of project. Would pull all dependencies in $dir/vendor.")

	commands.HandleArgs(c)
}

func getCurrentDir() string {
	ex, _ := os.Executable()
	return filepath.Dir(ex)
}

func getDir(dir *string) string {
	if dir != nil {
		return *dir
	}
	dir = findPackageFile(getCurrentDir())
	if dir == nil {
		log.Panicf("No git repository found in folder or parent folders.\n")
	}
	return *dir
}

func findPackageFile(dir string) *string {
	for dir != "." {
		println(dir)
		if fileExists(filepath.Join(dir, dependencyFilename)) {
			return &dir
		}
		nextDir, _ := filepath.Abs(dir + "/..")
		if dir == nextDir {
			break
		}
		dir = nextDir
	}
	return nil
}

func doInit(dir string) {
	depFile := filepath.Join(dir, dependencyFilename)
	if fileExists(depFile) {
		fmt.Printf("%s already exists: %s", dependencyFilename, depFile)
		return
	}
	files := getAllSourceFiles(dir)
	log.Printf("Found files: %d", len(*files))
	imports := getAllImports(files)
	packages := getImports(imports)
	dependencies := installPackages(packages, dir)
	data := bpmEntry{Dependencies: dependencies}
	writeDataFile(&data)
}

func doInstall(dir string) {
	depFile := filepath.Join(dir, dependencyFilename)
	if !fileExists(depFile) {
		fmt.Printf("%s does not exist: %s", dependencyFilename, depFile)
		return
	}
}

func doRebuild(dir string) {
	vendorDir := filepath.Join(dir, vendorFolderName)
	removeDir(vendorDir)
	files := getAllSourceFiles(dir)
	log.Printf("Found files: %d", len(*files))
	imports := getAllImports(files)
	packages := getImports(imports)
	dependencies := installPackages(packages, dir)
	data := bpmEntry{Dependencies: dependencies}
	writeDataFile(&data)
}

func getAllImports(files *[]string) []*ast.ImportSpec {
	var (
		bytes   []byte
		err     error
		f       *ast.File
		imports = []*ast.ImportSpec{}
	)
	for _, fname := range *files {
		if bytes, err = ioutil.ReadFile(fname); err != nil {
			log.Panic(err)
		}

		fs := token.NewFileSet()
		if f, err = parser.ParseFile(fs, "", string(bytes), parser.ImportsOnly); err != nil {
			log.Panic(err)
		}

		imports = append(imports, f.Imports...)
	}
	return imports
}

func getAllSourceFiles(dir string) *[]string {
	result := make([]string, 0)

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Panic(err)
	}

	for _, f := range files {
		fullName := filepath.Join(dir, f.Name())
		if f.IsDir() {
			if f.Name() == vendorFolderName {
				log.Printf("Skipping vendor folder: %s\n", fullName)
				continue
			}
			sources := getAllSourceFiles(fullName)
			if len(*sources) > 0 {
				result = append(result, *sources...)
			}
			continue
		}
		if strings.HasSuffix(fullName, ".go") {
			log.Printf("File: %s\n", fullName)
			result = append(result, fullName)
		}
	}
	return &result
}

func getImports(arr []*ast.ImportSpec) *[]string {
	pattern, err := regexp.Compile("^([^/]+\\.[^.]{1,6}/[^/]+/[^/]+)")
	if err != nil {
		log.Panic(err)
	}

	imports := make(map[string]*interface{}, 0)

	for _, i := range arr {
		val := (*i.Path).Value
		val = strings.Trim(val, `"`)
		if pattern.MatchString(val) {
			val = pattern.FindString(val)
			if _, ok := imports[val]; !ok {
				log.Printf("Found package: %s", val)
				imports[val] = nil
			}
		}
	}

	result := make([]string, 0)
	for key := range imports {
		key = strings.Trim(key, `"`)
		result = append(result, key)
	}
	return &result
}

type bpmEntry struct {
	URL          string               `json:"url,omitempty"`
	Branch       string               `json:"branch,omitempty"`
	Commit       string               `json:"commit,omitempty"`
	Dependencies map[string]*bpmEntry `json:"dependencies"`
}

type channelResult struct {
	pkg   string
	entry *bpmEntry
}

func installPackages(packages *[]string, dir string) map[string]*bpmEntry {
	vendorDir := filepath.Join(dir, vendorFolderName)
	createDir(vendorDir)

	dependencies := make(map[string]*bpmEntry, len(*packages))

	channelLis := []chan channelResult{}

	for _, filename := range *packages {

		pkgDir := filepath.Join(vendorDir, filepath.FromSlash(filename))
		createDir(pkgDir)

		c := make(chan channelResult, 1)
		go pullAndGetEntry(c, filename, pkgDir)
		channelLis = append(channelLis, c)
	}

	for _, c := range channelLis {
		result, ok := <-c
		if ok {
			log.Printf("Dependency pulled: %s", result.pkg)
			dependencies[result.pkg] = result.entry
		}
	}

	return dependencies
}

func pullAndGetEntry(c chan channelResult, pkg string, pkgDir string) {
	cloneURL := "https://" + pkg

	log.Printf("Pulling package %s in %s...", cloneURL, pkgDir)
	log.Println(cloneRepo(cloneURL, pkgDir))

	branch := getCurrentBranch(pkgDir)
	hash := getCurrentCommitHash(pkgDir)

	c <- channelResult{
		pkg: pkg,
		entry: &bpmEntry{
			URL:    cloneURL,
			Branch: branch,
			Commit: hash}}
}

func removeDir(dir string) {
	if fileExists(dir) {
		if err := os.RemoveAll(dir); err != nil {
			log.Fatal(err)
		}
	}
}

func createDir(dir string) {
	if !fileExists(dir) {
		err := os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

func runCmd(dir *string, command string, args ...string) []byte {
	var (
		out []byte
		err error
	)
	cmd := exec.Command(command, args...)
	if dir != nil {
		cmd.Dir = *dir
	}
	if out, err = cmd.CombinedOutput(); err != nil {
		log.Panic(err)
	}
	return out
}

func cloneRepo(url string, dir string) string {
	return string(runCmd(nil, "git", "clone", url, dir))
}

func getCurrentBranch(dir string) string {
	out := runCmd(&dir, "git", "branch")
	branch := string(regexp.MustCompile("\\* ([^\n]+)\n").Find(out))
	branch = strings.TrimLeft(branch, "* ")
	branch = strings.TrimRight(branch, "\n ")
	return branch
}

func getCurrentCommitHash(dir string) string {
	hash := string(runCmd(&dir, "git", "rev-parse", "HEAD"))
	hash = strings.TrimRight(hash, "\n ")
	return hash
}

func jsonEncodeIndented(deps *bpmEntry) []byte {
	buffer := bytes.Buffer{}
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(deps); err != nil {
		log.Panic(err)
	}
	return buffer.Bytes()
}

func writeDataFile(data *bpmEntry) {
	if err := ioutil.WriteFile(dependencyFilename, jsonEncodeIndented(data), os.ModeExclusive); err != nil {
		log.Panic(err)
	}
}

func isGitRepo(dir string) bool {
	return fileExists(filepath.Join(dir, gitFolderName))
}
