#!/bin/bash
# This file executes htmlproofer checks on the compiled html files in _site.

# Version of htmlproofer to use.
HP_VERSION=v0.2

# Local directories to ignore when checking external links
HP_IGNORE_LOCAL_DIRS=""

# URLs to ignore when checking external links.
HP_IGNORE_URLS="/docs.openshift.org/,#,/github.com\/projectcalico\/calico\/releases\/download/"

# jekyll uid
JEKYLL_UID=${JEKYLL_UID:=`id -u`}

# The htmlproofer check is flaky, so we retry a number of times if we get a bad result.
# If it doesn't pass once in 10 tries, we count it as a failed check.
echo "Running a hard URL check against recent releases"
for i in `seq 1 1`; do  # cnx htmlproofer not flakey so much as full of broken links
	echo "htmlproofer attempt #${i}"
	docker run -ti -e JEKYLL_UID=${JEKYLL_UID} --rm -v $(pwd)/_site:/_site/ quay.io/calico/htmlproofer:${HP_VERSION} /_site --file-ignore ${HP_IGNORE_LOCAL_DIRS} --assume-extension --check-html --empty-alt-ignore --url-ignore ${HP_IGNORE_URLS}

	# Store the RC for future use.
	rc=$?
	echo "htmlproofer rc: $rc"

	# If the command executed successfully, break out. Otherwise, retry. 
	if [[ $rc == 0 ]]; then break; fi

	# Otherwise, sleep a short period and then retry.
	echo "htmlproofer failed, retry in 10s"
	sleep 10
done

# Rerun htmlproofer across _all_ files, but ignore failure, allowing us to notice legacy docs issues without failing CI
echo "Running a soft check across all files"
docker run -ti -e JEKYLL_UID=${JEKYLL_UID} --rm -v $(pwd)/_site:/_site/ quay.io/calico/htmlproofer:${HP_VERSION} /_site --assume-extension --check-html --empty-alt-ignore --url-ignore "#"

# Exit using the return code from the loop above.
exit $rc
