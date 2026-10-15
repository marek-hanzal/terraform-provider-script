VERSION := 0.1.0
PLATFORMS := darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64 windows_arm64

.PHONY: crossbuild
crossbuild:
	@set -eu; for platform in $(PLATFORMS); do \
		os=$${platform%_*}; arch=$${platform#*_}; suffix=""; \
		if [ "$$os" = windows ]; then suffix=.exe; fi; \
		directory="build/mirror/registry.terraform.io/marek-hanzal/script/$(VERSION)/$$platform"; \
		mkdir -p "$$directory"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -mod=readonly -trimpath -buildvcs=false \
			-ldflags="-s -w -buildid= -X main.version=$(VERSION)" \
			-o "$$directory/terraform-provider-script_v$(VERSION)$$suffix" .; \
	done
