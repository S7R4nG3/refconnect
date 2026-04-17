## v0.7.3 (2026-04-17)

### Fix

- **internal/ui**: resolving a bunch of ui errors in logs

## v0.7.2 (2026-04-17)

### Fix

- **logging**: Addinga  fix for the log pruning that was nuking logs right after they were created

## v0.7.1 (2026-04-17)

### Fix

- **internal/**: some windows launch performance fixes

## v0.7.0 (2026-04-17)

### Feat

- **cz**: adding commitizen for versioning

## v0.6.1 (2026-04-16)

### Fix

- **aprs**: moving aprs toggle

## v0.6.0 (2026-04-16)

### Fix

- **wakelock**: adding a screen wakelock to prevent machines from falling asleep and losing connection

## v0.5.0 (2026-04-16)

### Feat

- **bluetooth**: adding bluetooth capabilities for linux

### Fix

- **internal/**: updating port selection dropdown to remove bluetooth devices - reverting to using serial devices once bluetooth is connected

## v0.4.1 (2026-04-15)

### Feat

- **docs**: updating documentation and simplifying config files

### Fix

- **README.md**: refreshing readme
- **README.md**: updating readme screenshot
- **.github/dependabot.yml**: udpating dependabot configurations

## v0.4.0 (2026-04-14)

### Feat

- **internal.**: TH-D75 support via USB-C - fixes for XRF/XLX reflectors - couple UI fixes

### Fix

- **Makefile**: updating test entrypoint for simpler logging

## v0.3.0 (2026-04-09)

### Feat

- **ui**: updating a bunch of UI elements for simplicity - updated documentation - added simple install scripts

## v0.2.1 (2026-04-09)

### Fix

- **README.md**: few more docs updates - adding a stub for a simple sh script for a one-liner install

## v0.2.0 (2026-04-09)

### Fix

- **internal**: fixed the connection to the radio - confirmed on REF reflector, working on other types

## v0.1.3 (2026-03-27)

### Fix

- **client.go**: fixing formatting error
- **release.yml**: adding signing to the macos package

## v0.1.2 (2026-03-27)

### Fix

- **release.yml**: still tweaking macos cicd pipeline

## v0.1.1 (2026-03-27)

### Fix

- **release.yml**: updating release jobs to build with latest macos

## v0.1.0 (2026-03-26)

### Feat

- **init**: repo init

### Fix

- **.gitignore**: adding ds store files to gitignore
- **internal/**: making some performance tweaks
