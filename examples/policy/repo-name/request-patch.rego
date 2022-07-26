package vignet.request.patch
import future.keywords

default allow := false

allow if {
	input.repo == "infra-test"
}
