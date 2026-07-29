.PHONY: portal-build speccheck

portal-build:
	@bash packaging/portal-build.sh

speccheck:
	@greatspectations check . || echo "Install: go install github.com/OpenTollGate/greatspectations/cmd/greatspectations@latest"
