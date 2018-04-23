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
	"net/url"
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
		pkg = ""
	)
	c.Name = "Basic Package Manager"
	c.MainCommand = "bpm"
	c.NewCommand("init", func() {
		doInit(getCurrentDir())
	}, "Creates a bpm.json file in the current directory and gets all dependencies.")
	c.NewCommand("install", func() {
		doInstall(getDir(&dir))
	}, "Pulls configured packages and version.")
	c.NewCommand("update", func() {
		doUpdate(getDir(&dir), pkg)
	}, "Updates all or a specific package by pulling the latest commit on the specified branch.")
	c.NewCommand("rebuild", func() {
		doRebuild(getDir(&dir))
	}, "Forgets all dependency data and pulls latest package versions.")
	c.NewArg("-d", &dir, getCurrentDir(), "Root dir of project. Would pull all dependencies in $dir/vendor.")
	c.NewArg("-p", &pkg, "", "Execute the specified command for a specific dependency package.")

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
	pkg := getCurrentPackage(dir)
	if pkg == "" {
		return
	}

	dependencies := resolveDependencies(dir, pkg)

	data := &bpmPackage{
		Package:      pkg,
		Dependencies: dependencies}
	writeDataFile(data)
}

func resolveDependencies(dir string, pkg string) map[string]*bpmEntry {
	files := getAllSourceFiles(dir)
	log.Printf("Found files: %d", len(*files))
	imports := getAllImports(files)
	packages := getImports(imports, pkg)
	dependencies := installPackages(packages, dir)

	for pkg, entry := range dependencies {
		pkgDir := filepath.Join(dir, vendorFolderName, pkg)
		log.Printf("Subpackage: %s", pkgDir)
		entry.Dependencies = resolveDependencies(pkgDir, pkg)
	}

	return dependencies
}

func doInstall(dir string) {
	depFile := filepath.Join(dir, dependencyFilename)
	if !fileExists(depFile) {
		fmt.Printf("%s does not exist: %s", dependencyFilename, depFile)
		return
	}
	data := readDataFile(depFile)
	pullPackages(data.Dependencies, dir)
	writeDataFile(data)
}

func doUpdate(dir string, pkg string) {

}

func doRebuild(dir string) {
	fmt.Printf("Working dir: %s\n", dir)
	pkg := getCurrentPackage(dir)
	if pkg == "" {
		return
	}
	vendorDir := filepath.Join(dir, vendorFolderName)
	removeDir(vendorDir)

	dependencies := resolveDependencies(dir, pkg)
	data := &bpmPackage{
		Package:      pkg,
		Dependencies: dependencies}
	writeDataFile(data)
}

func getAllImports(files *[]string) map[string][]*ast.ImportSpec {
	var (
		bytes   []byte
		err     error
		f       *ast.File
		imports = make(map[string][]*ast.ImportSpec)
	)
	for _, fname := range *files {
		if bytes, err = ioutil.ReadFile(fname); err != nil {
			log.Panic(err)
		}

		fs := token.NewFileSet()
		if f, err = parser.ParseFile(fs, "", string(bytes), parser.ImportsOnly); err != nil {
			log.Panic(err)
		}

		imports[fname] = f.Imports
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

func getPackagePattern() *regexp.Regexp {
	pattern, err := regexp.Compile("^([^/]+\\.[^.]{1,6}/[^/]+/[^/]+)")
	if err != nil {
		log.Panic(err)
	}
	return pattern
}

func getImports(importMap map[string][]*ast.ImportSpec, currentPkg string) *[]string {

	pattern := getPackagePattern()
	imports := make(map[string]*interface{}, 0)

	for fname, arr := range importMap {
		for _, i := range arr {
			val := (*i.Path).Value
			val = strings.Trim(val, `"`)
			if pattern.MatchString(val) {
				val = pattern.FindString(val)
				if _, ok := imports[val]; !ok {
					log.Printf("Found package: %s in file %s", val, fname)
					imports[val] = nil
				}
			}
		}
	}

	result := make([]string, 0)
	for key := range imports {
		key = strings.Trim(key, `"`)
		if key != currentPkg {
			result = append(result, key)
		}
	}
	return &result
}

type bpmPackage struct {
	Package      string               `json:"package"`
	Dependencies map[string]*bpmEntry `json:"dependencies"`
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

	channelList := []chan channelResult{}

	for _, filename := range *packages {

		pkgDir := filepath.Join(vendorDir, filepath.FromSlash(filename))
		createDir(pkgDir)

		c := make(chan channelResult, 1)
		go clonePackage(c, filename, pkgDir)
		channelList = append(channelList, c)
	}

	for _, c := range channelList {
		result, ok := <-c
		if ok && result.entry != nil {
			log.Printf("Dependency pulled: %s", result.pkg)
			dependencies[result.pkg] = result.entry
		}
	}

	return dependencies
}

func pullPackages(dependencies map[string]*bpmEntry, dir string) {

	if dependencies == nil || len(dependencies) == 0 {
		return
	}

	vendorDir := filepath.Join(dir, vendorFolderName)
	createDir(vendorDir)

	channelMap := make(map[string]chan error, 0)

	for pkg, data := range dependencies {
		pkgDir := filepath.Join(vendorDir, pkg)

		c := make(chan error, 1)
		go pullPackage(c, pkg, data, pkgDir)
		channelMap[pkg] = c
	}

	for pkg, c := range channelMap {
		err, ok := <-c
		if ok {
			if err != nil {
				log.Panic(err)
			}
			log.Printf("Dependency pulled: %s", pkg)
			data := dependencies[pkg]
			pkgDir := filepath.Join(vendorDir, pkg)
			pullPackages(data.Dependencies, pkgDir)
		}
	}
}

func pullPackage(c chan error, pkg string, entry *bpmEntry, pkgDir string) {

	if !fileExists(pkgDir) {
		createDir(pkgDir)
	}

	if !isGitRepo(pkgDir) {
		cloneRepo(entry.URL, pkgDir)
	}

	pullRepo(entry, pkgDir)

	c <- nil
}

func clonePackage(c chan channelResult, pkg string, pkgDir string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Errorf("Couldn't clone package %s doe to error: %s", pkg, r)
		}
		c <- channelResult{
			pkg:   pkg,
			entry: nil}
	}()

	cloneURL := "https://" + pkg

	cloneRepo(cloneURL, pkgDir)

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

func runCmd(dir *string, getOutput bool, command string, args ...string) []byte {
	var (
		out []byte
		err error
	)
	cmd := exec.Command(command, args...)
	log.Printf("Command: %s %s", command, strings.Join(args, " "))
	if dir != nil {
		cmd.Dir = *dir
	}
	if !getOutput {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			log.Panic(err)
		}
		return make([]byte, 0)
	}

	if out, err = cmd.CombinedOutput(); err != nil {
		log.Panic(err)
	}
	return out
}

func pullRepo(entry *bpmEntry, pkgDir string) {

	log.Printf("Pulling package %s in %s", entry.URL, pkgDir)

	branch := getCurrentBranch(pkgDir)
	if entry.Branch == "" {
		entry.Branch = branch
	}
	if branch != entry.Branch {
		checkoutBranch(pkgDir, entry.Branch)
	}
	commit := getCurrentCommitHash(pkgDir)
	if entry.Commit == "" {
		entry.Commit = commit
	}
	if commit != entry.Commit {
		checkoutCommit(pkgDir, entry.Commit)
	}
}

func checkoutBranch(pkgDir string, branch string) {
	runCmd(&pkgDir, false, "git", "checkout", branch)
}

func checkoutCommit(pkgDir string, commit string) {
	runCmd(&pkgDir, false, "git", "checkout", commit, ".")
}

func cloneRepo(url string, dir string) {
	log.Printf("Cloning package %s in %s...", url, dir)
	runCmd(nil, false, "git", "clone", url, dir)
}

func getCurrentBranch(dir string) string {
	out := runCmd(&dir, true, "git", "branch")
	branch := string(regexp.MustCompile("\\* ([^\n]+)\n").Find(out))
	branch = strings.TrimLeft(branch, "* ")
	branch = strings.TrimRight(branch, "\n ")
	return branch
}

func getCurrentCommitHash(dir string) string {
	hash := string(runCmd(&dir, true, "git", "rev-parse", "HEAD"))
	hash = strings.TrimRight(hash, "\n ")
	return hash
}

func jsonEncodeIndented(deps *bpmPackage) []byte {
	buffer := bytes.Buffer{}
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(deps); err != nil {
		log.Panic(err)
	}
	return buffer.Bytes()
}

func writeDataFile(data *bpmPackage) {
	if err := ioutil.WriteFile(dependencyFilename, jsonEncodeIndented(data), os.ModeExclusive); err != nil {
		log.Panic(err)
	}
}

func readDataFile(filename string) *bpmPackage {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Panic(err)
	}
	data := bpmPackage{}
	err = json.Unmarshal(bytes, &data)
	if err != nil {
		log.Panic(err)
	}
	return &data
}

func isGitRepo(dir string) bool {
	return fileExists(filepath.Join(dir, gitFolderName))
}

func getCurrentPackage(dir string) string {
	result := string(runCmd(&dir, true, "git", "remote", "get-url", "origin"))
	u, err := url.Parse(result)
	if err != nil {
		fmt.Println("Could not resolve current repo origin: ", err.Error())
		return ""
	}
	pkg := u.Hostname() + u.RawPath
	pkg = strings.TrimSpace(pkg)
	if strings.HasSuffix(pkg, ".git") {
		pkg = pkg[:len(pkg)-4]
	}
	pattern := getPackagePattern()
	if pattern.MatchString(pkg) {
		return pattern.FindString(pkg)
	}
	fmt.Println("Repo origin is not a valid package: " + pkg)
	return ""
}
