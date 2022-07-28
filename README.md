# Vignet
The missing GitOps piece: expose a Git repository behind an authenticated API to perform updates with authorization.

## Where does it fit in?

GitOps tools already handle many aspects of synching infrastructure resources (e.g. in Kubernetes) from Git repositories.
To fully integrate into the delivery workflow, **updates to the Git repo should be able to be performed via an API for automation** (e.g. set a new image tag for a release).
Since a Git repo can store the complete infrastructure, updates should be protected - an application pipeline should only be allowed to update its own declaration.

This is why we created _Vignet_:

* It runs as a standalone service in your infrastructure
* It has access to infrastructure repositories
* It exposes an authenticated API for patching declarations via commands
* It integrates flexible authorization via OPA (Open Policy Agent) rules to decide if a command should be allowed
* Easy to integrate into GitLab and other services
* Works with Flux, ArgoCD or other GitOps tools

## Design principles

* Vignet is stateless, repositories and authorization are configured via configuration files
