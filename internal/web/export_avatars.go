// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"archive/zip"
	"github.com/airencracken/comfylib/profileimage"
)

func writeAvatarEntries(archive *zip.Writer, account *exportAccount, picture profileimage.Picture) error {
	for _, entry := range []struct {
		name string
		data []byte
	}{{"avatar.png", picture.Still}, {"avatar.gif", picture.Animation}} {
		if len(entry.data) == 0 {
			continue
		}
		if entry.name == "avatar.png" {
			account.AvatarPath = entry.name
		} else {
			account.AnimatedAvatarPath = entry.name
		}
		writer, err := archive.Create(entry.name)
		if err == nil {
			_, err = writer.Write(entry.data)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
