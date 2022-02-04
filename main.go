package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "embed"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerClient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	bopsdk "github.com/bopmatic/sdk/golang"
	"gopkg.in/yaml.v2"
)

const defaultProjectFilename = "Bopmatic.yaml"
const buildImageName = "bopmatic/build:latest"

type commonOpts struct {
	projectFilename string
}

var subCommandTab = map[string]func(args []string){
	"build":    buildMain,
	"deploy":   deployMain,
	"describe": describeMain,
	"destroy":  destroyMain,
	"help":     helpMain,
	"config":   configMain,
	"new":      newMain,
	"version":  versionMain,
}

func buildMain(args []string) {
	type buildOpts struct {
		common commonOpts
	}

	var opts buildOpts

	f := flag.NewFlagSet("bopmatic build", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err := f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	proj, err := bopsdk.NewProject(opts.common.projectFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if proj.Desc.BuildCmd == "" {
		fmt.Printf("Project %v is a static site only; no build required\n",
			proj.Desc.Name)
		os.Exit(0)
	}

	err = os.Chdir(proj.Desc.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to chdir %v: %v\n", proj.Desc.Root, err)
		os.Exit(1)
	}

	err = runContainerCommand([]string{proj.Desc.BuildCmd}, os.Stdout,
		os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build %v: %v\n", proj.Desc.Name, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully built %v\n", proj.Desc.Name)
}

func deployMain(args []string) {
	fmt.Fprintf(os.Stderr, "deploy Unimplemented\n")
	os.Exit(1)
}

func describeMain(args []string) {
	type describeOpts struct {
		common commonOpts
	}

	var opts describeOpts

	f := flag.NewFlagSet("bopmatic describe", flag.ExitOnError)
	setCommonFlags(f, &opts.common)
	err := f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	proj, err := bopsdk.NewProject(opts.common.projectFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%v", proj)
}

func destroyMain(args []string) {
	fmt.Fprintf(os.Stderr, "destroy Unimplemented")
	os.Exit(1)
}

//go:embed help.txt
var helpText string

func helpMain(args []string) {
	fmt.Printf(helpText)
}

func configMain(args []string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not find user home directory: %v\n", err)
		os.Exit(1)
	}
	credsPath := filepath.Join(homeDir, ".config", "bopmatic", "creds.yaml")
	err = os.MkdirAll(path.Dir(credsPath), 0700)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not create config directory %v: %v\n",
			path.Dir(credsPath), err)
		os.Exit(1)
	}

	type Config struct {
		AccessKeyId     string `yaml:"keyid"`
		AccessKeySecret string `yaml:"keysecret"`

		FormatVersion string `yaml:"format"`
	}

	var config Config

	file, err := os.Open(credsPath)
	if err == nil {
		decoder := yaml.NewDecoder(file)
		err = decoder.Decode(&config)
		file.Close()

		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse existing %v: %w; Removing...",
				credsPath, err)
			_ = os.Remove(credsPath)
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Could not open %v: %v\n", credsPath, err)
		os.Exit(1)
	}

	config.FormatVersion = "1.0"

	fmt.Printf("Enter Bopmatic Access Key ID [%v]: ", config.AccessKeyId)
	fmt.Scanf("%s", &config.AccessKeyId)
	config.AccessKeyId = strings.TrimSpace(config.AccessKeyId)

	fmt.Printf("Enter Bopmatic Access Key Secret [%v]: ", config.AccessKeySecret)
	fmt.Scanf("%s", &config.AccessKeySecret)
	config.AccessKeySecret = strings.TrimSpace(config.AccessKeySecret)

	fileContent, err := yaml.Marshal(&config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not yaml encode %v: %v\n", credsPath, err)
		os.Exit(1)
	}

	err = ioutil.WriteFile(credsPath, fileContent, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not write %v: %v\n", credsPath, err)
		os.Exit(1)
	}

	if hasBopmaticBuildImage() {
		fmt.Printf("Update Bopmatic Build Image? (Y/N) [Y]: ")
	} else {
		fmt.Printf("Bopmatic needs to download the Bopmatic Build Image in order to build projects. It is roughly 1GiB in size.\n")
		fmt.Printf("Download Bopmatic Build Image? (Y/N) [Y]: ")
	}
	shouldDownload := "Y"
	fmt.Scanf("%s", &shouldDownload)
	shouldDownload = strings.TrimSpace(shouldDownload)

	if strings.ToUpper(shouldDownload)[0] == 'Y' {
		pullBopmaticImage()
	}
}

const dockerInstallErrMsg = "Could not invoke docker; please double check that you have docker installed: %v\n"

func pullBopmaticImage() {
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv,

		dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, dockerInstallErrMsg, err)
		os.Exit(1)
	}

	reader, err := cli.ImagePull(context.Background(), buildImageName,
		types.ImagePullOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to pull image: %v", err)
		os.Exit(1)
	}

	defer reader.Close()

	// cli.ImagePull() returns newline separated JSON documents; parse
	// them so we can show more human friendly output to the user
	type ProgressDetail struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	}
	type DockerStatus struct {
		Status string         `json:"status"`
		Id     string         `json:"id"`
		Detail ProgressDetail `json:"progressDetail"`
	}

	var dockerStatus DockerStatus
	progressScanner := bufio.NewScanner(reader)
	for progressScanner.Scan() {
		err = json.Unmarshal(progressScanner.Bytes(), &dockerStatus)
		if err != nil {
			continue
		}

		var progressPct uint64
		progressPct = 100
		if dockerStatus.Detail.Total != 0 {
			progressPct = (dockerStatus.Detail.Current * 100) / dockerStatus.Detail.Total
		}

		fmt.Printf("\t%v id:%v progress:%v%%\n", dockerStatus.Status, dockerStatus.Id,
			progressPct)
	}

	err = progressScanner.Err()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to pull image: %v", err)
		os.Exit(1)
	}
}

func hasBopmaticBuildImage() bool {
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv,
		dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, dockerInstallErrMsg, err)
		os.Exit(1)
	}

	ctx := context.Background()
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFunc()

	images, err := cli.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list images: %v", err)
		os.Exit(1)
	}

	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == buildImageName {
				return true
			}
		}
	}

	return false
}

func runContainerCommand(cmdAndArgs []string, stdOut io.Writer,
	stdErr io.Writer) error {

	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv,
		dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf(dockerInstallErrMsg, err)
	}

	pwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("Could not get current working dir: %w", err)
	}

	hostConfig := &container.HostConfig{
		AutoRemove: true,
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: pwd,
				Target: pwd,
			},
		},
	}

	containerConfig := &container.Config{
		User:       fmt.Sprintf("%v:%v", os.Geteuid(), os.Getegid()),
		Cmd:        cmdAndArgs,
		Image:      buildImageName,
		WorkingDir: pwd,
	}

	ctx := context.Background()
	resp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil,
		nil, "")
	if err != nil {
		return fmt.Errorf("Failed to create container: %w", err)
	}

	err = cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("Failed to run container: %w", err)
	}

	logOutputOpts := types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	}
	logOutput, err := cli.ContainerLogs(ctx, resp.ID, logOutputOpts)
	if err != nil {
		return fmt.Errorf("Failed to get container output: %w", err)
	}
	defer logOutput.Close()

	// the container's stdout and stderr are muxed into a Docker specific
	// output format; so we demux them here
	stdcopy.StdCopy(stdOut, stdErr, logOutput)

	statusCh, errCh := cli.ContainerWait(ctx, resp.ID,
		container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("Container run failed: %w\n", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("%v failed with status:%v", cmdAndArgs[0],
				status.StatusCode)
		}
	}

	return nil
}

func isGoodProjectName(projectName string) bool {
	if projectName == "" {
		return false
	}
	projectName = strings.ToLower(projectName)
	url, err := url.ParseRequestURI("https://" + projectName + ".bopmatic.com")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Sorry %v.bopmatic.com is not a valid endpoint\n",
			projectName)
		return false
	}
	_, err = net.LookupIP(url.Host)
	if err == nil {
		fmt.Fprintf(os.Stderr, "Sorry %v is already taken\n", url.Host)
		return false
	}

	_, err = os.Stat(projectName)
	if err == nil {
		fmt.Fprintf(os.Stderr, "Sorry %v already exists in your current directory\n",
			projectName)

		return false
	} // else

	return true
}

type ProjTemplate struct {
	name    string
	srcPath string
}

func selectProjectTemplateIdx(tmplNameIn string,
	templateList []ProjTemplate) int {

	if tmplNameIn == "" {
		return -1
	}

	for idx, _ := range templateList {
		if templateList[idx].name == tmplNameIn {
			return idx
		}
	}

	fmt.Fprintf(os.Stderr, "%v is not a valid project template\n", tmplNameIn)
	return -1
}

func newMain(args []string) {
	if !hasBopmaticBuildImage() {
		fmt.Fprintf(os.Stderr, "Could not find Bopmatic Build Image; please run:\n\n\tbopmatic config\n")
		os.Exit(1)
	}

	// 1 fetch templates from Bopmatic Build Image
	var templateList []ProjTemplate
	templateListBuf := new(bytes.Buffer)
	err := runContainerCommand([]string{"ls", "/bopmatic/examples/golang"},
		templateListBuf, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to retrieve list of templates: %v\n",
			err)
		os.Exit(1)
	}
	for _, tmpl := range strings.Split(templateListBuf.String(), "\n") {
		if tmpl != "" {
			templateList = append(templateList,
				ProjTemplate{name: tmpl,
					srcPath: "/bopmatic/examples/golang/" + tmpl})
		}
	}
	templateList = append(templateList,
		ProjTemplate{name: "static_site", srcPath: "/bopmatic/examples/static_site"})

	// 2 get user inputs
	var projectName string
	for !isGoodProjectName(projectName) {
		fmt.Printf("Enter Bopmatic Project Name []: ")
		fmt.Scanf("%s", &projectName)
		projectName = strings.TrimSpace(projectName)
	}

	fmt.Printf("Available project templates:\n")
	for _, tmpl := range templateList {
		fmt.Printf("\t%v\n", tmpl.name)
	}

	var templateName string
	var selectedTmplIdx int
	for selectedTmplIdx = -1; selectedTmplIdx == -1; selectedTmplIdx = selectProjectTemplateIdx(templateName, templateList) {

		const defaultTemplateName = "helloworld"
		templateName = defaultTemplateName

		fmt.Printf("Enter Bopmatic Project Template [%v]: ", defaultTemplateName)
		fmt.Scanf("%s", &templateName)
		templateName = strings.TrimSpace(templateName)
	}

	// 3 copy project from template
	err = runContainerCommand([]string{"cp", "-r",
		templateList[selectedTmplIdx].srcPath, "./" + projectName}, os.Stdout,
		os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create project %v: %v", projectName,
			err)
		os.Exit(1)
	}

	// 4 set the created project's name
	projectDir := filepath.Join(".", projectName)
	projectFile := filepath.Join(projectDir, "Bopmatic.yaml")

	// @todo find a cleaner way to replace the project name
	sedSwapExpr := fmt.Sprintf("s/  name.*/  name: \"%v\"/", projectName)
	err = runContainerCommand([]string{"sed", "-i", sedSwapExpr, projectFile},
		os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set project name %v: %v", projectName,
			err)
		os.Exit(1)
	}

	// 5 validate everything worked
	proj, err := bopsdk.NewProject(projectFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Created project %v but it fails to parse: %v",
			projectDir, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully created .%v%v:\n%v", string(os.PathSeparator),
		projectDir, proj.String())
}

//go:embed version.txt
var versionText string

func versionMain(args []string) {
	fmt.Printf("bopmatic-cli-%v", versionText)
}

func setCommonFlags(f *flag.FlagSet, o *commonOpts) {
	f.StringVar(&o.projectFilename, "projfile", defaultProjectFilename,
		"Bopmatic project filename")
}

func main() {
	exitStatus := 0

	subCommandName := "help"
	if len(os.Args) > 1 {
		subCommandName = os.Args[1]
	} else {
		exitStatus = 1
	}

	subCommand, ok := subCommandTab[subCommandName]
	if !ok {
		subCommand = helpMain
		exitStatus = 1
	}

	var args []string
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}

	subCommand(args)

	os.Exit(exitStatus)
}
