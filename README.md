# Bopmatic CLI

The Bopmatic CLI is a set of tools & utilities for interacting with
Bopmatic projects from the shell. 

## Building

make

## Installing

The Bopmatic CLI depends on docker being installed and runnable as non-root. 

### Ubuntu 20.04 LTS

Note these instructions and the CLI should work on any Linux
distribution, but have only been explicitly tested on Ubuntu 20.04
LTS.

```bash
$ wget https://github.com/bopmatic/cli/releases/download/v0.9.6/bopmatic
$ chmod 755 bopmatic
$ sudo mv bopmatic /usr/local/bin
```

### MacOS

```bash
$ brew install bopmatic/macos/cli
```

### Windows

1. Install WSL 2 (https://wslstorestorage.blob.core.windows.net/wslblob/wsl_update_x64.msi)
2. Set WSL default version
```powershell
wsl –set-default-version 2
```
3. Install Ubuntu 20.04 on WSL 2 (https://www.microsoft.com/store/apps/9n6svws3rx71)
3. Open an Ubuntu terminal window and follow Ubuntu 20.04 LTS install instructions above


## Usage

```bash
$ bopmatic --help
## Usage

Bopmatic - The easy button for serverless

To begin working with Bopmatic, run the `bopmatic new` command:

    $ bopmatic new

This will prompt you to create a new project in your language of choice.

The most common commands from there are:

    - bopmatic package build    : Build your project locally
    - bopmatic package deploy   : Send your project to Bopmatic ServiceRunner for deployment
    - bopmatic package describe : Request details regarding your deployed package
    - bopmatic package destroy  : Remove a previously sent deployment from Bopmatic ServiceRunner

For more information, please visit the project page: https://www.bopmatic.com/docs/

Usage:
  bopmatic [command]

Available Commands:
  describe       Describe the contents of a Bopmatic project
  package        Create, Deploy, Destroy, or List Bopmatic project packages
                   run 'bopmatic package help' for more details
  help           This help screen
  config         Set Bopmatic configuration
  new            Create a new Bopmatic project
  version        Print Bomatic CLI's version number

Common Flags:
  --projfile                         Bopmatic project file; defaults to Bopmatic.yaml
```

## Contributing
Pull requests are welcome at https://github.com/bopmatic/cli

For major changes, please open an issue first to discuss what you
would like to change.

## License
[AGPL3](https://www.gnu.org/licenses/agpl-3.0.en.html)
