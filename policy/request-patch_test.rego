package vignet.request.patch
import future.keywords

test_commands_path_match_claim_project_path if {
    count(violations) == 0 with input as {
        "repo": "infra-test",
        "patchRequest": {
            "commands": [{
                "path": "my-group/my-project/release.yaml"
            }]
        },
        "authCtx": {
            "gitLabClaims": {"project_path": "my-group/my-project"}
        }
    }
}

test_commands_path_doesnt_match_claim_project_path if {
    v := violations with input as {
        "repo": "infra-test",
        "patchRequest": {
            "commands": [{
                "path": "my-group/other-project/release.yaml"
            }]
        },
        "authCtx": {
            "gitLabClaims": {"project_path": "my-group/my-project"}
        }
    }
    v[_] == "path \"my-group/other-project/release.yaml\" is not a prefix of GitLab project path (\"my-group/my-project\")"
}
