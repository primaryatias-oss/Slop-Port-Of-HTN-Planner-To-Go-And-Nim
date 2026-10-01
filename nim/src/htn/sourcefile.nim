## Reads domain and world-state files the way the original std::ifstream-based
## readers do.

import std/posix

proc readSourceFile*(path: string, text: var string): bool =
  ## Reads `path` into `text` and returns true when the path can be opened.
  ## Like std::ifstream on Linux, a path that opens but cannot be read (a
  ## directory) yields empty text rather than an error.
  let fd = posix.open(path.cstring, O_RDONLY or O_CLOEXEC)
  if fd < 0: return false
  text = ""
  var buffer: array[65536, char]
  while true:
    let count = posix.read(fd, addr buffer[0], buffer.len)
    if count < 0 and errno == EINTR: continue
    if count <= 0: break
    let used = text.len
    text.setLen(used + count)
    copyMem(addr text[used], addr buffer[0], count)
  discard posix.close(fd)
  true
