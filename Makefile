VERSION := 0.2.0
PLATFORMS := darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64 windows_arm64

.PHONY: crossbuild release
crossbuild:
	@set -eu; \
	mkdir -p build; staging=$$(mktemp -d build/.crossbuild.XXXXXX); \
	trap 'rm -rf "$$staging"' EXIT HUP INT TERM; \
	root="build/mirror/registry.terraform.io/marek-hanzal/script/$(VERSION)"; \
	for platform in $(PLATFORMS); do \
		os=$${platform%_*}; arch=$${platform#*_}; suffix=""; \
		if [ "$$os" = windows ]; then suffix=.exe; fi; \
		name="terraform-provider-script_v$(VERSION)$$suffix"; \
		mkdir -p "$$staging/$$platform"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -mod=readonly -trimpath -buildvcs=false \
			-ldflags="-s -w -buildid= -X main.version=$(VERSION)" \
			-o "$$staging/$$platform/$$name" .; \
		if [ -e "$$root/$$platform/$$name" ] && ! cmp -s "$$staging/$$platform/$$name" "$$root/$$platform/$$name"; then \
			echo "Refusing to replace different bytes under pinned version $(VERSION) ($$platform). Bump VERSION." >&2; exit 1; \
		fi; \
	done; \
	mkdir -p "$$root"; \
	for platform in $(PLATFORMS); do \
		for staged in "$$staging/$$platform/"*; do \
			target="$$root/$$platform/$${staged##*/}"; \
			if [ ! -e "$$target" ]; then mkdir -p "$$root/$$platform"; cp "$$staged" "$$target"; fi; \
		done; \
	done; \
	(cd "$$root" && shasum -a 256 */terraform-provider-script_v* > SHA256SUMS); \
	cat "$$root/SHA256SUMS"

release: crossbuild
	python3 scripts/package-release.py --version "$(VERSION)"
