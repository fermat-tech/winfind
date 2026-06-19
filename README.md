# winfind

A Unix-like `find` command for Windows, written in Go.

Rename the binary to anything you like — all usage and error messages derive from the executable name automatically.

## Installation

### Download

Grab the latest binary from [Releases](https://github.com/fermat-tech/winfind/releases) and put it somewhere on your `PATH`.

### Build from source

Requires [Go](https://golang.org) 1.21+.

```powershell
git clone https://github.com/fermat-tech/winfind.git
cd winfind
go build -o winfind.exe .
```

## Usage

```
winfind [path...] [expression]
```

Paths default to `.` if omitted.

## Predicates

| Flag | Description |
|------|-------------|
| `-name PATTERN` | Filename glob (case-sensitive) |
| `-iname PATTERN` | Filename glob (case-insensitive) |
| `-path PATTERN` | Full path glob |
| `-ipath PATTERN` | Full path glob (case-insensitive) |
| `-type f\|d\|l` | File, directory, or symlink |
| `-size [+/-]N[ckMG]` | Size filter — `c`=bytes, `k`=KB, `M`=MB, `G`=GB; `+N` means greater than, `-N` means less than |
| `-mtime [+/-]N` | Modified N days ago; `+N`=older than, `-N`=newer than |
| `-newer FILE` | Modified more recently than FILE |
| `-empty` | Empty file or empty directory |
| `-maxdepth N` | Descend at most N directory levels |
| `-mindepth N` | Skip entries fewer than N levels deep |

## Operators

Higher to lower precedence:

```
! PRED              negate
-not PRED           negate (long form)
PRED1 PRED2         implicit AND
PRED1 -and PRED2    explicit AND
PRED1 -or  PRED2    OR
( PRED )            grouping
```

## Actions

If no action is specified, `-print` is the default.

| Flag | Description |
|------|-------------|
| `-print` | Print path followed by newline |
| `-print0` | Print path followed by null byte (for use with `xargs -0`) |
| `-ls` | Print size, mode, modification time, and path |
| `-delete` | Delete matched file or empty directory |
| `-exec CMD {} ;` | Run CMD for each match — `{}` is replaced by the path |

## Examples

```powershell
# Find all Go source files
winfind . -name "*.go"

# Find files larger than 1 MB
winfind C:\src -type f -size +1M

# Find log files modified in the last 7 days and delete them
winfind . -mtime -7 -name "*.log" -delete

# List all files with details
winfind . -type f -ls

# Find files NOT matching a pattern
winfind . -not -name "*.exe"

# Combine conditions with OR
winfind . -iname "*.jpg" -or -iname "*.png"

# Run a command on each match
winfind . -name "*.txt" -exec notepad {} ;

# Limit search depth
winfind . -maxdepth 2 -type f

# Find empty directories
winfind . -type d -empty
```

## License

MIT
