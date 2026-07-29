GO ?= go
PREFIX ?= /usr/local
DESTDIR ?=
BUILD_DIR ?= build
APP_ID := io.github.dweymouth.supersonic
BINARY_NAME := supersonic-desktop
BINARY := $(BUILD_DIR)/$(BINARY_NAME)

BINDIR = $(DESTDIR)$(PREFIX)/bin
DATADIR = $(DESTDIR)$(PREFIX)/share
APPLICATIONSDIR = $(DATADIR)/applications
ICONDIR = $(DATADIR)/icons/hicolor
METAINFODIR = $(DATADIR)/metainfo

.PHONY: all build clean install install-bin install-desktop install-icons install-metainfo \
	user-install uninstall user-uninstall refresh-desktop-cache lint \
	package_macos bundledeps_macos_homebrew bundledeps_macos_macports \
	bundledeps_macos_highsierra zip_macos package_windows package_linux

all: build

build:
	mkdir -p "$(BUILD_DIR)"
	$(GO) build -tags migrated_fynedo -trimpath -o "$(BINARY)" .

clean:
	rm -f "$(BINARY)"
	rmdir "$(BUILD_DIR)" 2>/dev/null || true

install: build install-bin install-desktop install-icons install-metainfo refresh-desktop-cache

install-bin:
	install -Dm755 "$(BINARY)" "$(BINDIR)/$(BINARY_NAME)"

install-desktop:
	desktop-file-validate res/supersonic-desktop.desktop
	install -Dm644 res/supersonic-desktop.desktop "$(APPLICATIONSDIR)/$(APP_ID).desktop"

install-icons:
	install -Dm644 res/appicon-128.png "$(ICONDIR)/128x128/apps/$(APP_ID).png"
	install -Dm644 res/appicon-256.png "$(ICONDIR)/256x256/apps/$(APP_ID).png"
	install -Dm644 res/appicon-512.png "$(ICONDIR)/512x512/apps/$(APP_ID).png"

install-metainfo:
	install -Dm644 res/io.github.dweymouth.supersonic.appdata.xml "$(METAINFODIR)/$(APP_ID).metainfo.xml"

# Installs into ~/.local, which Wofi and other XDG launchers scan without root.
user-install: PREFIX := $(HOME)/.local
user-install: install

uninstall:
	rm -f "$(BINDIR)/$(BINARY_NAME)"
	rm -f "$(APPLICATIONSDIR)/$(APP_ID).desktop"
	rm -f "$(ICONDIR)/128x128/apps/$(APP_ID).png"
	rm -f "$(ICONDIR)/256x256/apps/$(APP_ID).png"
	rm -f "$(ICONDIR)/512x512/apps/$(APP_ID).png"
	rm -f "$(METAINFODIR)/$(APP_ID).metainfo.xml"
	$(MAKE) refresh-desktop-cache PREFIX="$(PREFIX)" DESTDIR="$(DESTDIR)"

user-uninstall: PREFIX := $(HOME)/.local
user-uninstall: uninstall

refresh-desktop-cache:
	@if [ -z "$(DESTDIR)" ]; then \
		command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$(PREFIX)/share/applications" || true; \
		command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -t -f "$(PREFIX)/share/icons/hicolor" || true; \
	fi

# dylibbundler doesn't seem to pick up on the Python framework dependency,
# so the last 3 cmds move it over manually. This is a bit fragile though
# since it assumes a specific location and version of the dependency
package_macos:
	fyne package -os darwin -tags migrated_fynedo

bundledeps_macos_homebrew:
	dylibbundler -od -b -x ./Supersonic.app/Contents/MacOS/supersonic -d ./Supersonic.app/Contents/Frameworks/ -p @executable_path/../Frameworks/
	./scripts/copy_python_dep_osx.sh
	codesign --force --deep --preserve-metadata=entitlements,requirements,flags,runtime --sign - "./Supersonic.app/Contents/MacOS/supersonic"

bundledeps_macos_macports:
	dylibbundler -od -b -x ./Supersonic.app/Contents/MacOS/supersonic -d ./Supersonic.app/Contents/Frameworks/ -p @executable_path/../Frameworks/
	codesign --force --deep --preserve-metadata=entitlements,requirements,flags,runtime --sign - "./Supersonic.app/Contents/MacOS/supersonic"

bundledeps_macos_highsierra:
	mkdir ./Supersonic.app/Contents/Frameworks
	cp -R res/libs/mac_x64/mpv/* ./Supersonic.app/Contents/Frameworks
	install_name_tool -change "/usr/local/opt/mpv/lib/libmpv.2.dylib" "@executable_path/../Frameworks/libmpv.2.dylib" "./Supersonic.app/Contents/MacOS/supersonic"

zip_macos:
	zip --symlinks -r Supersonic.zip Supersonic.app/

package_windows:
	fyne package -os windows -tags migrated_fynedo

package_linux:
	fyne package -os linux -tags migrated_fynedo

lint:
	golangci-lint run
