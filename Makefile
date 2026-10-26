BINARY  = refconnect
EXE     = refconnect.exe
APP     = RefConnect.app
ICON    = docs/antenna.png

# macOS linker warns about duplicate libraries; suppress with ldflags.
# Linux and Windows do not need this. Check $(OS) first because uname
# may not exist when make runs under cmd.exe/PowerShell.
ifeq ($(OS),Windows_NT)
  LDFLAGS =
else ifeq ($(shell uname -s),Linux)
  LDFLAGS =
else
  LDFLAGS = -ldflags="-extldflags=-Wl,-no_warn_duplicate_libraries"
endif

.PHONY: run build build-windows bundle icons-windows install-linux clean

linux-packages:
	sudo apt install -y libxi-dev libxinerama-dev libxrandr-dev libxcursor-dev libxxf86vm-dev

build:
	@go build $(LDFLAGS) -o $(BINARY) .

icons-windows:
	@go install github.com/tc-hib/go-winres@latest
	@go-winres simply --icon $(ICON) --arch amd64

# Windows GUI build (no console window) with the embedded icon, matching the
# release workflow. Requires MinGW gcc on PATH for cgo (e.g. C:\msys64\mingw64\bin).
# Go silently disables cgo when gcc is missing, which surfaces as a confusing
# "build constraints exclude all Go files" error from go-gl; forcing it on
# makes Go report the missing compiler instead.
build-windows: export CGO_ENABLED = 1
build-windows: icons-windows
	@go build -ldflags="-s -w -H=windowsgui" -o $(EXE) .

# Install a desktop entry + icon for the locally built binary (Linux only).
DATA_HOME ?= $(or $(XDG_DATA_HOME),$(HOME)/.local/share)
install-linux: build
	@install -Dm644 $(ICON) $(DATA_HOME)/icons/hicolor/32x32/apps/refconnect.png
	@mkdir -p $(DATA_HOME)/applications
	@sed 's|^Exec=.*|Exec="$(CURDIR)/$(BINARY)"|' configs/refconnect.desktop > $(DATA_HOME)/applications/refconnect.desktop

bundle: build
	@rm -rf $(APP)
	@mkdir -p $(APP)/Contents/MacOS
	@mkdir -p $(APP)/Contents/Resources
	@cp $(BINARY) $(APP)/Contents/MacOS/
	@cp configs/Info.plist $(APP)/Contents/
	@mkdir -p AppIcon.iconset
	@sips -z 16 16     $(ICON) --out AppIcon.iconset/icon_16x16.png
	@sips -z 32 32     $(ICON) --out AppIcon.iconset/icon_16x16@2x.png
	@sips -z 32 32     $(ICON) --out AppIcon.iconset/icon_32x32.png
	@sips -z 64 64     $(ICON) --out AppIcon.iconset/icon_32x32@2x.png
	@sips -z 128 128   $(ICON) --out AppIcon.iconset/icon_128x128.png
	@sips -z 256 256   $(ICON) --out AppIcon.iconset/icon_128x128@2x.png
	@sips -z 256 256   $(ICON) --out AppIcon.iconset/icon_256x256.png
	@sips -z 512 512   $(ICON) --out AppIcon.iconset/icon_256x256@2x.png
	@sips -z 512 512   $(ICON) --out AppIcon.iconset/icon_512x512.png
	@iconutil -c icns AppIcon.iconset --output $(APP)/Contents/Resources/AppIcon.icns
	@rm -rf AppIcon.iconset

run: bundle
	@open $(APP)

clean:
	@rm -f $(BINARY) $(EXE)
	@rm -f rsrc_windows_amd64.syso
	@rm -rf $(APP)

test: build
	@rm -f ./refconnect.log
	@./$(BINARY)
	@mv $${HOME}/.config/refconnect/Logs/*.log ./refconnect.log

update:
	@go get -u ./...