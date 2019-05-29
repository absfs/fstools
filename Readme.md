# FsTools - Tools for file systems.

Work in progress.

## Features

- Walk
    + Walk walks a absfs.FileSystem similar to the filepath Walk standard
      library function.

- WalkWithOptions
    + Adds the ability to specify options for how a walk is performed. Such as
      if directories are sorted and in what order, normal or fast walk,
      traversal order, symbolic link handling and more.

- PreOrder, PostOrder, InOrder and BreadthFirst Walkers
    + Additional walking strategies for ordered traversal of file systems.

- Copy
    + Copy copies filesystem structures form one filesystem to another with
      options for selecting and transforming what is copied.

- Describe
    + Creates a data structure that describes a file system. The description
      contains basic metadata as found in `os.FileInfo`, the identification
      of file data and directories using a (c4 Id)[https://github.com/Avalanche-io/c4],
      and can be serialized into JSON, YAML, CUE and other formats.

- Diff
    + Returns the difference between two file system descriptions.

- Patch
    + Given a file system that matches the first file system in a Diff, and a
      c4 data source Patch will transform the file system to match the second
      file system in a diff. Requires a c4 data source.

- Apply
    + Apply takes a file system, c4 data source, and file system description and
      transforms the file system to match the description.


