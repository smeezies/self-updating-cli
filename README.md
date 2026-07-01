# Self Updating binary

### Order of operations
 - New code is pushed to the repo
 - A release is created when a branch is tagged
 - New binaries are generated via Github action pipelines with go releaser
 - CLI will check if there are new versions available
    - Check on startup
    - Have a routine that checks
 - CLI will download the latest release
 - CLI unpackages tarballs
 - CLI will replace the existing binary
   - Restart?
   - Replacement strategy?
 - CLI with latest binary runs 