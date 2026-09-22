#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later

imvault_missing_tools=
for imvault_video_tool in "${IMVAULT_FFMPEG:-ffmpeg}" "${IMVAULT_FFPROBE:-ffprobe}"; do
	if ! command -v "$imvault_video_tool" >/dev/null 2>&1; then
		imvault_missing_tools="$imvault_missing_tools $imvault_video_tool"
	fi
done

if [ -n "$imvault_missing_tools" ]; then
	printf '\nWarning: missing video tools:%s\n' "$imvault_missing_tools"
	printf '%s\n' \
		'Video uploads will use placeholder thumbnails and cannot be checked for duration.' \
		'Install ffmpeg (including ffprobe) to enable video thumbnails.' \
		'On Gentoo: emerge --ask media-video/ffmpeg' \
		'Restart imvault after installing; existing placeholders are not regenerated automatically.'
fi
