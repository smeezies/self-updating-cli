# Self Updating binary

### Order of operations
 - New code is pushed to the repo
 - New binaries are generated via pipelines
 - CLI will check if there are new versions available
    - Check on startup?
    - Have a routine that checks?
 - CLI will download the latest binary
 - CLI will replace the existing binary
   - Restart?
   - Replacement strategy?
 - CLI with latest binary runs 