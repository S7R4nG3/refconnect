BINARY  = refconnect
APP     = RefConnect.app
ICON    = docs/antenna.png

# macOS linker warns about duplicate libraries; suppress with ldflags.
# Linux does not need this.
ifeq ($(shell uname -s),Linux)
  LDFLAGS =
else
  LDFLAGS = -ldflags="-extldflags=-Wl,-no_warn_duplicate_libraries"
endif

.PHONY: run build bundle icons-windows clean

linux-packages:
	sudo apt install -y libxi-dev libxinerama-dev libxrandr-dev libxcursor-dev libxxf86vm-dev

build:
	@go build $(LDFLAGS) -o $(BINARY) .

icons-windows:
	@go install github.com/tc-hib/go-winres@latest
	@go-winres simply --icon $(ICON) --arch amd64

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
	@rm -f $(BINARY)
	@rm -f rsrc_windows_amd64.syso
	@rm -rf $(APP)

test: build
	@rm -f ./refconnect.log
	@rm -f $${HOME}/.config/refconnect/Logs/*.log
	@./$(BINARY)
	@mv $${HOME}/.config/refconnect/Logs/*.log ./refconnect.log