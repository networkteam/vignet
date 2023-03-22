package vignet.request.patch
import future.keywords

commands := input.patchRequest.commands
gitLabProjectPath := input.authCtx.gitLabClaims.project_path

commandPathNotPrefixOfGitLabProjectPath contains cmd if {
    some cmd in commands
    not startswith(cmd.path, sprintf("projects/%s/", [gitLabProjectPath]))
}

commandPathIsNotYaml contains cmd if {
    some cmd in commands
    not glob.match("**/*.{yml,yaml}", ["/"], cmd.path)
}

violations contains msg if {
	some cmd in commandPathNotPrefixOfGitLabProjectPath
    msg := sprintf("path %q is not a prefix of GitLab project path (projects/%q)", [cmd.path, gitLabProjectPath])
}

violations contains msg if {
	some cmd in commandPathIsNotYaml
    msg := sprintf("path %q is not a YAML file", [cmd.path])
}
