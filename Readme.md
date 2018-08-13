# FsTools - Tools for file systems.

Work in progress.

## Features

- Walk 
    + Walk walks a absfs.FileSystem similar to the filepath Walk standard library function.

- WalkWithOptions
    + Adds the ability to specify options for how a walk is performed. Such as if directories are sorted and in what order, normal or fast walk, traversal order, symbolic link handling and more.

- Copy
    Copy copies filesystem structures form one filesystem to another with a options to filter what is copied, and transform the data during the copy process.


