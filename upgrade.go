/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "embed"

	"github.com/docker/docker/api/types/image"
	dockerClient "github.com/docker/docker/client"

	"github.com/bopmatic/sdk/golang/util"
)

func getLatestVersion() (string, error) {
	const LatestReleaseUrl = "https://api.github.com/repos/bopmatic/cli/releases/latest"

	client := http.Client{
		Timeout: time.Second * 30,
	}

	resp, err := client.Get(LatestReleaseUrl)
	if err != nil {
		return "", err
	}

	releaseJsonDoc, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var releaseDoc map[string]any
	err = json.Unmarshal(releaseJsonDoc, &releaseDoc)
	if err != nil {
		return "", err
	}

	latestRelease, ok := releaseDoc["tag_name"].(string)
	if !ok {
		return "", fmt.Errorf("Could not parse %v", LatestReleaseUrl)
	}

	if isBrewVersion() {
		latestRelease += BrewVersionSuffix
	}
	return latestRelease, nil
}

func upgradeMain(args []string) {
	upgradeBuildContainer(args)
	upgradeCLI(args)
}

func upgradeCLI(args []string) {
	if versionText == DevVersionText {
		fmt.Fprintf(os.Stderr, "Skipping CLI upgrade on development version\n")
		return
	}
	latestVer, err := getLatestVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not determine latest version: %v\n", err)
		os.Exit(1)
	}
	if latestVer == versionText {
		fmt.Printf("Bopmatic CLI %v is already the latest version\n",
			versionText)
		return
	}

	fmt.Printf("A new version of the Bopmatic CLI is available (%v). Upgrade? (Y/N) [Y]: ",
		latestVer)
	shouldUpgrade := "Y"
	fmt.Scanf("%s", &shouldUpgrade)
	shouldUpgrade = strings.ToUpper(strings.TrimSpace(shouldUpgrade))

	if shouldUpgrade[0] != 'Y' {
		return
	}

	fmt.Printf("Upgrading bopmatic cli from %v to %v...\n", versionText,
		latestVer)

	if isBrewVersion() {
		upgradeCLIViaBrew()
	} else {
		upgradeCLIViaGithub(latestVer)
	}
}

func upgradeBuildContainer(args []string) {
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if haveBuildImg {
		needUpgrade, err :=
			util.DoesLocalImageNeedUpdate(util.BopmaticImageRepo,
				util.BopmaticImageTag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if needUpgrade == false {
			fmt.Printf("Bopmatic Build container is up to date\n")
			return
		}

		fmt.Printf("Update Bopmatic Build Image? (Y/N) [Y]: ")
	} else {
		fmt.Printf("Bopmatic needs to download the Bopmatic Build Image in order to build projects. It is roughly 975MiB(compressed) in size.\n")
		fmt.Printf("Download Bopmatic Build Image? (Y/N) [Y]: ")
	}
	shouldDownload := "Y"
	fmt.Scanf("%s", &shouldDownload)
	shouldDownload = strings.TrimSpace(shouldDownload)

	if strings.ToUpper(shouldDownload)[0] == 'Y' {
		pullBopmaticImage()

		if !haveBuildImg {
			fmt.Printf("To create a bopmatic project, next run:\n\t'bopmatic new'\n")
		}
	}
}

func upgradeCLIViaBrew() {
	ctx := context.Background()
	err := util.RunHostCommand(ctx, []string{"brew", "update"}, os.Stdout,
		os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update brew formulae: %v\n", err)
		os.Exit(1)
	}
	err = util.RunHostCommand(ctx, []string{"brew", "install",
		"bopmatic/macos/cli"}, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to upgrade bopmatic: %v\n", err)
		os.Exit(1)
	}
}

func upgradeCLIViaGithub(latestVer string) {
	const LatestDownloadFmt = "https://github.com/bopmatic/cli/releases/download/%v/bopmatic"

	client := http.Client{
		Timeout: time.Second * 30,
	}

	resp, err := client.Get(fmt.Sprintf(LatestDownloadFmt, latestVer))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}

	tmpFile, err := os.CreateTemp("", "bopmatic-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create temp file: %v", err)
	}
	binaryContent, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}

	_, err = tmpFile.Write(binaryContent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}
	err = tmpFile.Chmod(0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}
	err = tmpFile.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download version %v: %v\n",
			versionText, err)
		os.Exit(1)
	}
	myBinaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not determine path to bopmatic CLI: %v\n",
			err)
		os.Exit(1)
	}
	myBinaryPath, err = filepath.EvalSymlinks(myBinaryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not determine path to bopmatic CLI: %v\n",
			err)
		os.Exit(1)
	}

	myBinaryPathBak := myBinaryPath + ".bak"
	err = os.Rename(myBinaryPath, myBinaryPathBak)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not replace existing %v; do you need to be root?: %v\n",
			myBinaryPath, err)
		os.Exit(1)
	}
	err = os.Rename(tmpFile.Name(), myBinaryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not replace existing %v; do you need to be root?: %v\n",
			myBinaryPath, err)
		_ = os.Rename(myBinaryPathBak, myBinaryPath)
		os.Exit(1)
	}
	_ = os.Remove(myBinaryPathBak)

	fmt.Printf("Upgrade %v to %v complete\n", myBinaryPath, latestVer)
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
		util.BopmaticBuildImageName, image.PullOptions{})
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
			progressPct =
				(dockerStatus.Detail.Current * 100) / dockerStatus.Detail.Total
		}

		fmt.Printf("\t%v id:%v progress:%v%%\n", dockerStatus.Status,
			dockerStatus.Id, progressPct)
	}

	err = progressScanner.Err()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to pull image: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully pulled %v\n", util.BopmaticBuildImageName)
}

//go:embed version.txt
var versionText string

const DevVersionText = "v0.devbuild"

func versionMain(args []string) {
	fmt.Printf("bopmatic-cli-%v\n", versionText)
}

func isBrewVersion() bool {
	if versionText[len(versionText)-1] == BrewVersionSuffix[0] {
		return true
	}

	return false
}

func checkAndPrintUpgradeCLIWarning() bool {
	if versionText == DevVersionText {
		return false
	}
	latestVer, err := getLatestVersion()
	if err != nil {
		return false
	}
	if latestVer == versionText {
		return false
	}

	fmt.Fprintf(os.Stderr, "*WARN*: A new version of the Bopmatic CLI is available (%v). Please upgrade via 'bopmatic upgrade'.\n",
		latestVer)

	return true
}

func checkAndPrintUpgradeContainerWarning() bool {
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil || !haveBuildImg {
		return false
	}

	needUpgrade, err := util.DoesLocalImageNeedUpdate(util.BopmaticImageRepo,
		util.BopmaticImageTag)
	if err != nil || needUpgrade == false {
		return false
	}

	fmt.Fprintf(os.Stderr, "*WARN*: A new version of the Bopmatic Build container is available. Please upgrade via 'bopmatic upgrade'.\n")

	return true
}
