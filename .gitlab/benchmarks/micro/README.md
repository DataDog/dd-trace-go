# How to run microbenchmarks locally

Install [bp-runner](https://github.com/DataDog/benchmarking-platform-tools/blob/main/bp-runner/INSTALL.md), if you don't have it yet.

Then copy-paste `.env.example` to `.env`, and update configuration as needed.

Then run:

```bash
bp-runner bp-runner.yml --debug -t --local
```

In order to run interactively, use:

```bash
bp-runner bp-runner.yml --debug -t --local -i

## and then inside container run, you can use
export GO_VERSION=$(go version | sed 's/go version //') && bp-runner bp-runner.yml --debug
```

### I have "Permission denied (publickey)." on git clone in Docker container build

Primarily ensure that the SSH agent on your host machine is running and that the SSH key is added to it.

For example, run new ssh agent and add your key to it:

```bash
eval $(ssh-agent -s) 
ssh-add ~/.ssh/<YOUR_KEY_NAME_HERE>
ssh-add -l
```

Ensure `ssh-add -l` command should show key fingerprint that matches the one in your Github account.

If this doesn't help, check Github docs on troubleshooting SSH key setup https://docs.github.com/en/authentication/troubleshooting-ssh/error-permission-denied-publickey.
