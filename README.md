# Vignet
The missing GitOps piece: expose a Git repository behind an authenticated API to perform updates with authorization.

## Where does it fit in?

GitOps tools already handle many aspects of syncing infrastructure resources (e.g. in Kubernetes) from Git repositories.
To fully integrate into the delivery workflow, **updates to the Git repo should be able to be performed via an API for automation** (e.g. set a new image tag for a release).
Since a Git repo can store the complete infrastructure, updates should be protected - an application pipeline should only be allowed to update its own declaration.

This is why we created _Vignet_:

* It runs as a standalone service in your infrastructure
* It will get access to your GitOps repositories
* It exposes an authenticated Rest API for patching YAML declarations via commands
* It integrates flexible authorization via OPA (Open Policy Agent) rules to decide if a command should be allowed
* It is easy to integrate into GitLab CI, GitHub Actions and other systems
* Works perfectly with Flux, ArgoCD or other GitOps tools

## Design principles

* Vignet is stateless, repositories and authorization are configured via configuration files
* Policies are customizable via Open Policy Agent (OPA) rules

## Current state

It is in the early stages of development, but it should already be usable for
integration in GitLab CI.
Configuration and API is subject to change.
Use in production at your own risk.

## Command reference

```
NAME:
   vignet - The missing GitOps piece: expose Git repositories for automation via an authenticated HTTP API

USAGE:
   vignet [global options] command [command options] [arguments...]

DESCRIPTION:
   The default command starts an HTTP server that handles commands.

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --help, -h  show help (default: false)

   authorization
   --policy value  Path to an OPA policy bundle path, uses the built-in by default [$VIGNET_POLICY]

   configuration
   --config value, -c value  Path to the configuration file (default: "config.yaml") [$VIGNET_CONFIG]

   http
   --address value  Address for HTTP server to listen on (default: ":8080") [$VIGNET_ADDRESS]

   logging
   --force-logfmt  Force logging to use logfmt (default: false) [$VIGNET_FORCE_LOGFMT]
   --verbose       Enable verbose logging (default: false) [$VIGNET_VERBOSE]

```

## Configuration

Vignet is configured via flags or env vars and a YAML configuration file.
The configuration file makes it easier to manage multiple repository configurations.
A custom Open Policy Agent policy bundle can be used to customize authorization via the `policy` flag.

### Example configuration

```yaml
authenticationProvider:
  # Use a GitLab job token authentication provider
  type: gitlab

  # Configuration for the GitLab authentication provider
  gitlab:
    # URL to the GitLab instance
    url: https://gitlab.example.com

# Configure repositories that can be accessed by Vignet
repositories:
  # Repository name
  my-project:
    # URL to the repository
    url: https://gitlab.example.com/my-group/my-project.git
    basicAuth:
      # Username doesn't matter for GitLab
      username: gitlab
      # Use an access token with scopes "read_repository", "write_repository"
      password: an-access-token

commit:
  # Default message to use for a commit if none is specified in a request
  defaultMessage: "Automated update"
  # Default author to use for a commit if none is specified in a request
  defaultAuthor:
    name: Git autopilot
    email: bot@example.com
```

## Rest API

### POST `/patch/{repository}`

Pulls the repository, patches files according to commands, creates a commit and pushes to the repository.

Responds with status code 200 on success.

#### Body

* `commit` *object* Commit options (optional)
  * `message` *string* Commit message (optional)
  * `committer` *object* Committer for the commit (optional)
    * `name` *string*
    * `email` *string*
  * `author` *object* Author for the commit (optional)
    * `name` *string*
    * `email` *string*
* `commands` *array* Commands to perform, one of `setField` and `n.n.` must be set
  * `path` *string* Path to the file to patch (relative from repository root)
  * `setField` *object* Perform a **set field command** (optional)
    * `field` *string* Field to set with dot path syntax, JSONPath features are supported (see examples)
    * `value` *mixed* Value to set the field to
    * `create` *boolean* Create the field (and intermediate path) if it doesn't exist (optional, defaults to false)
  * `createFile` *object* Perform a **create file command** to create a new file (optional)
    * `content` *string* Content of the file to create
  * `deleteFile` *object* Perform a **delete file command** to delete a file (optional)

#### Examples

##### Setting a field in a YAML file

```http request
POST http://localhost:8080/patch/infra-test
Authorization: Bearer [CI_JOB_JWT]
Content-Type: application/json

{
  "commit": {
    "message": "Bump image to 1.2.5"
  },
  "commands": [
    {
      "path": "my-group/my-project/release.yml",
      "setField": {
        "field": "spec.values.image.tag",
        "value": "1.2.5"
      }
    }
  ]
}
```

##### Using JSONPath

[JSONPath](https://github.com/vmware-labs/yaml-jsonpath#references) can be used to reference a field by array index, filter expression or other features:

```http request
POST http://localhost:8080/patch/infra-test
Authorization: Bearer [CI_JOB_JWT]
Content-Type: application/json

{
  "commit": {
    "message": "Bump image to 1.2.5, update BUILD_ID"
  },
  "commands": [
    {
      "path": "my-group/my-project/deployment.yml",
      "setField": {
        "field": "spec.template.spec.containers[0].image",
        "value": "registry.example.com/my/image:1.2.5"
      },
      "setField": {
        "field": "spec.template.spec.containers[0].env[?(@.name == 'BUILD_ID')].value",
        "value": "987654"
      }
    }
  ]
}
```

Using Curl is a convenient way to integrate Vignet into GitLab CI:

```shell
curl -s --fail-with-body -H "Authorization: Bearer $CI_JOB_JWT" -H "Content-Type: application/json" -d @- http://localhost:8080/patch/infra-test << JSON
{
  "commit": {
    "message": "${CI_PROJECT_PATH}: Release ${CI_REGISTRY_TAG} to ${CI_ENVIRONMENT_SLUG}"
  },
  "commands": [
    {
      "path": "projects/${CI_PROJECT_PATH}/release-${CI_ENVIRONMENT_SLUG}.yaml",
      "setField": {
        "field": "spec.values.app.image.tag",
        "value": "$CI_REGISTRY_TAG"
      }
    }
  ]
}
JSON
```

##### Writing a new file

```http request
POST http://localhost:8080/patch/infra-test
Authorization: Bearer [CI_JOB_JWT]
Content-Type: application/json

{
  "commit": {
    "message": "Bump image to 1.2.5"
  },
  "commands": [
    {
      "path": "my-group/my-project/new.yml",
      "createFile": {
        "content": "---\nversion: 1.2.3\n"
      }
    }
  ]
}
```

Curl can be paired with `jq` to read an existing file and convert it to a JSON string:

```shell
# Prepare a YAML file for the new release before this command
yaml_file=new-release.yml

curl --fail-with-body -H "Authorization: Bearer $CI_JOB_JWT"  http://localhost:8080/patch/infra-test -H "Content-Type: application/json" -d \
@<(jq -n --arg yaml "$(jq -sR . $yaml_file)" "$(cat <<JSON
{
  "commit": {
    "message": "${CI_PROJECT_PATH}: Release ${CI_REGISTRY_TAG} to ${CI_ENVIRONMENT_SLUG}"
  },
  "commands": [
    {
      "path": "projects/${CI_PROJECT_PATH}/new-${CI_ENVIRONMENT_SLUG}.yaml",
      "createFile": {
        "content": \$yaml
      }
    }
  ]
}
JSON
)")
```

## Authentication

### GitLab

* GitLab CI generates a job token env var `CI_JOB_JWT` for each job. It contains claims about the user, project and repository.
* This token needs to be passed via `Authorization: Bearer [CI_JOB_JWT]` header to Vignet.
* Requests are denied if the token is invalid or missing.
* Claims in the token are passed to the authorization policy to check if the request should be allowed.

## Authorization

Vignet will pass the authentication context and request information to the policy for decision.

### Default policy

#### Patch request

* `path` Accepts only `.yml` and `.yaml` files

The further policy behavior depends on the authentication provider:

#### GitLab

* `path` Requires a prefix of the GitLab project path (of the job passing the job token).

  E.g. a job token with `project_path: "my-group/my-project"` will only authorize requests for `my-group/my-project/**/*.{yml,yaml}`.

## Known limitations

* Currently, only authentication via a GitLab job token is supported
* There is only a `setField` command for now

## License

[MIT](./LICENSE)
