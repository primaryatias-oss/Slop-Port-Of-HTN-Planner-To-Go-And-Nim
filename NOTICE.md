# Notice

This repository contains Go and Nim ports of
[HTN Planner](https://github.com/urosidoki/htn_planner). It is a derivative
work: the ports translate the original's C++ sources, and the
[Domains](Domains), [WorldStates](WorldStates) and
[HTNDiagnosticTests](HTNDiagnosticTests) directories are copied from it. The
reference build in [tools/oracle](tools/oracle) compiles the original sources
from a separate checkout; they are not included here.

HTN Planner is distributed under the following license:

```
MIT License

Copyright (c) 2026 Jose Antonio Escribano joseantonioescribanoayllon@gmail.com Sandra Alvarez sandruskiag@gmail.com

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

Files of the original keep their file-level copyright notices. As the
original's NOTICE.md explains, files carrying the 2023 joint notice are
attributed to Sandra Alvarez and Jose Antonio Escribano, and files carrying a
2023 or 2026 Jose Antonio Escribano notice are attributed as stated in them.

The ports themselves are distributed under the MIT License in
[LICENSE](LICENSE).
