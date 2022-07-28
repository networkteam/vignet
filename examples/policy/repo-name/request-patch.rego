package vignet.request.patch
import future.keywords

violations contains msg if {
	input.repo != "infra-test"
    msg := "repo must be infra-test"
}
