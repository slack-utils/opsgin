# What is it

Utility for integrating on-duty `Opsgenie` and `Slack`

Works in two modes:
- sync: syncs duty users from specific `Opsgenie` schedules with user groups in `Slack`.
- daemon: calling on-duty in `Slack` channels and sending them notifications in `Opsgenie` via alerts

## Licensing

To use the application, a license file is required. You can request a 14-day trial access to the application by contacting opsgin.app@gmail.com.

## What is required for work

- `Opsgenie` api key with access:
  - `configuration access`
  - `read`
- `Slack` app OAuth token with scopes:
  - `usergroups:read`
  - `usergroups:write`
  - `users:read`
  - `users:read.email`
- Docker / golang

## Configuration example

```yaml
# sync mode
slack_user_group_name1:
  - opsgenie schedule name 1
  - additional.user@num1
  - additional.user@num2
slack_user_group_name2:
  - opsgenie schedule name 2
  - additional.user@num1
  - additional.user@num2

# daemon mode
slack_app_name1:
  opsgenie:
    schedule: opsgenie schedule name 1
  slack:
    api_key: xoxb-***
    app_key: xapp-***
    user_group: user group name 1
slack_app_name2:
  opsgenie:
    schedule: opsgenie schedule name 2
  slack:
    api_key: xoxb-***
    app_key: xapp-***
    user_group: user group name 2
```

## Usage with docker

- Create a `config.yaml` in e.g. `/opt/opsgin` with the following content:

```yaml
slack_user_group_name1:
  - opsgenie schedule name 1
```

- Start the container by adding a directory with a configuration file:

```shell
docker run \
    -v /opt/opsgin:/opt/opsgin \
    -e OPSGIN_API_KEY=*** \
    -e OPSGIN_SLACK_API_KEY=*** \
    opsgin/opsgin:0.1-e6f2c10 sync
```

## Build from source code

```shell
go install -v github.com/opsgin/opsgin@0.1
opsgin
Utility for integrating on-duty Opsgenie and Slack

Usage:
  opsgin [command]

Available Commands:
  daemon      Calling on-duty in Slack channels and sending them notifications in Opsgenie via alerts
  help        Help about any command
  sync        Synchronization of the on-duty Opsgenie with Slack user groups

Flags:
      --config-file string   Set the configuration file name (default "config.yaml")
      --config-path string   Set the configuration file path (default "/Users/dkhalturin/repos/home/slack-utils/opsgin/build/package/etc/opsgin")
  -h, --help                 help for opsgin
      --log-format string    Set the log format: text, json (default "text")
      --log-level string     Set the log level: debug, info, warn, error, fatal (default "info")
      --log-pretty           Json logs will be indented
  -v, --version              version for opsgin

Use "opsgin [command] --help" for more information about a command.
```
