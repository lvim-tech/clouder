package main

// starter is the config.toml that `clouder --init` writes. Heavily commented,
// like configer's, so the file itself teaches the format.
const starter = `# clouder — pairs of (local folder ⇄ cloud folder), kept in step both ways.
#
# Getting a Dropbox account connected (one-time):
#   1. Go to https://www.dropbox.com/developers/apps and "Create app".
#      - API: Scoped access.
#      - Access: "Full Dropbox" (so 'remote' below can be any path). Pick
#        "App folder" instead if you want clouder confined to one app folder;
#        then 'remote' is relative to that folder and "" means its root.
#      - Permissions tab: enable files.metadata.read, files.metadata.write,
#        files.content.read, files.content.write, then Submit.
#   2. Copy the "App key" into app_key below. There is NO app secret — clouder
#      uses PKCE, so nothing secret ever lands in this file.
#   3. Run:  clouder --add-account dropbox <account-name>
#      It prints a URL; open it, approve, paste the code back. The refresh
#      token is stored in pass under clouder/dropbox/<account-name>.
#
# Then add one or more [[pair]] blocks and run 'clouder --diff' to preview.

[providers.dropbox]
app_key = "PASTE_YOUR_DROPBOX_APP_KEY"

# --- example pairs (edit or delete) ---

# [[pair]]
# name     = "notes"                    # unique; also names the state file
# local    = "__HOME__/Documents/notes" # ~ is accepted too
# provider = "dropbox"
# account  = "default"                  # the name you passed to --add-account
# remote   = "/notes"                   # cloud-side folder; "" = account root
# ignore   = ["*.tmp", "node_modules"]  # extra globs on top of the defaults

# [[pair]]
# name     = "wallpapers"
# local    = "~/Pictures/wallpapers"
# provider = "dropbox"
# account  = "default"
# remote   = "/wallpapers"
`
