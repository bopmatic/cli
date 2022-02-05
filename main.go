package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/docker/docker/api/types"
	dockerClient "github.com/docker/docker/client"

	bopsdk "github.com/bopmatic/sdk/golang"
	"github.com/bopmatic/sdk/golang/util"

	"gopkg.in/yaml.v2"
)

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

	err = proj.Build(os.Stdout, os.Stderr)
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
			fmt.Fprintf(os.Stderr, "Failed to parse existing %v: %v; Removing...",
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

	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if !haveBuildImg {
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

func pullBopmaticImage() {
	cli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv,

		dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		err := fmt.Errorf(util.DockerInstallErrMsg, err)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	reader, err := cli.ImagePull(context.Background(),
		util.BopmaticBuildImageName, types.ImagePullOptions{})
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
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if !haveBuildImg {
		fmt.Fprintf(os.Stderr, "Could not find Bopmatic Build Image; please run:\n\n\tbopmatic config\n")
		os.Exit(1)
	}

	// 1 fetch templates from Bopmatic Build Image
	var templateList []ProjTemplate
	templateListBuf := new(bytes.Buffer)
	ctx := context.Background()
	err = util.RunContainerCommand(ctx,
		[]string{"ls", "/bopmatic/examples/golang"}, templateListBuf, os.Stderr)
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
	for {
		fmt.Printf("Enter Bopmatic Project Name []: ")
		fmt.Scanf("%s", &projectName)
		projectName = strings.TrimSpace(projectName)
		isGoodName, reason := bopsdk.IsGoodProjectName(projectName)
		if isGoodName {
			break
		} else {
			fmt.Fprintf(os.Stderr, "%v\n", reason)
		}
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
	err = util.RunContainerCommand(ctx, []string{"cp", "-r",
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
	err = util.RunContainerCommand(ctx, []string{"sed", "-i", sedSwapExpr,
		projectFile}, os.Stdout, os.Stderr)
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
	f.StringVar(&o.projectFilename, "projfile", bopsdk.DefaultProjectFilename,
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
