#!/bin/sh
# Copy a sedit folder to S3. The files are already encrypted, so they go up as
# they are; nothing is decrypted and no password is needed.
#
# usage: scripts/vault-backup.sh [-n] [-d] [-c CLASS] DIR S3_URL
#   DIR     the folder to back up, e.g. ~/vault
#   S3_URL  where to put it, e.g. s3://my-bucket/vault
#   -n      show what would be uploaded, change nothing
#   -d      put the copy in a dated folder, S3_URL/YYYYMMDDTHHMM (local time), so
#           that every run is a complete copy of its own
#   -c      S3 storage class, e.g. STANDARD_IA (default: the bucket's)
#
# example usage:
#
#   vault-backup.sh ~/vault/ s3://space-backups/vault/
#
# The AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_DEFAULT_REGION and
# AWS_ENDPOINT_URL environment variables are used, as for any aws command (the
# aws CLI reads AWS_ENDPOINT_URL itself).
#
# Files deleted from the folder are NOT deleted from the bucket. Turn on bucket
# versioning (and a lifecycle rule to expire old versions) so that an overwrite
# or a bad edit can be undone too. The bucket sees file and folder names, sizes,
# times and the titles and descriptions, which sedit stores in the clear.
set -eu

usage() { echo "usage: $0 [-n] [-d] [-c CLASS] DIR S3_URL" >&2; exit 2; }

dry=
dated=
class=
while getopts ndc: opt; do
	case $opt in
	n) dry=--dryrun ;;
	d) dated=1 ;;
	c) class=$OPTARG ;;
	*) usage ;;
	esac
done
shift $((OPTIND - 1))
[ $# -eq 2 ] || usage
dir=$1
dest=$2

log() { printf '%s vault-backup: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
die() { log "$*" >&2; exit 1; }

command -v aws >/dev/null 2>&1 || die "the aws command is not installed"
[ -d "$dir" ] || die "$dir is not a folder"
: "${AWS_ACCESS_KEY_ID:?not set}" "${AWS_SECRET_ACCESS_KEY:?not set}" "${AWS_DEFAULT_REGION:?not set}" "${AWS_ENDPOINT_URL:?not set}"
case $dest in s3://?*) ;; *) die "the destination must look like s3://bucket/prefix" ;; esac
dest=${dest%/}/
[ -z "$dated" ] || dest=$dest$(date '+%Y%m%dT%H%M')/

# One run at a time (mkdir is atomic). A stale lock from a crash is removed by hand.
lock=${TMPDIR:-/tmp}/vault-backup.lock
mkdir "$lock" 2>/dev/null || die "another backup is running (or remove $lock)"
trap 'rmdir "$lock"' EXIT

# Left out: sedit's advisory lock files (.FILE.lock), its temporary files while a
# save is in flight, and the Finder's litter. "*" matches "/" too, so each pattern
# comes twice: for the top folder and for folders below it.
set -- \
	--exclude '.*.lock' --exclude '*/.*.lock' \
	--exclude '.sedit-*' --exclude '*/.sedit-*' \
	--exclude '.DS_Store' --exclude '*/.DS_Store'
[ -z "$class" ] || set -- "$@" --storage-class "$class"

# Versioning is what makes this a backup and not just a mirror: say so if it is off.
# (Checking it needs s3:GetBucketVersioning; without it we just carry on.)
bucket=${dest#s3://}
bucket=${bucket%%/*}
state=$(aws s3api get-bucket-versioning --bucket "$bucket" --query Status --output text 2>/dev/null || true)
case $state in
Enabled) ;;
"") log "note: could not check whether versioning is on for $bucket" ;;
*) log "warning: versioning is not enabled on $bucket; an overwritten file cannot be recovered" ;;
esac

log "${dry:+(dry run) }copying $dir to $dest"
# shellcheck disable=SC2086 # $dry is empty or one option
aws s3 sync "$dir" "$dest" "$@" $dry --only-show-errors || die "the copy failed"

if [ -z "$dry" ]; then
	# Check: a second pass must find nothing left to upload.
	left=$(aws s3 sync "$dir" "$dest" "$@" --dryrun) || die "could not verify the copy"
	if [ -n "$left" ]; then
		printf '%s\n' "$left" >&2
		die "some files are still not in the bucket (listed above)"
	fi
fi
log "done"
