# third_party/patches

Local changes to the pinned llama.cpp (the submodule `../llama.cpp`), as
patch files — never a fork. `../build-llama.sh` applies every `*.patch`
here in name order before it builds (a patch already in the tree is
skipped). Empty: the pin is upstream llama.cpp as py's llama_cpp_python
vendors it.
