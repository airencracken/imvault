// SPDX-License-Identifier: AGPL-3.0-or-later
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { join } from 'node:path';
const directory = execFileSync('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/airencracken/comfylib'], { encoding: 'utf8', env: { ...process.env, GOWORK: 'off' } }).trim();
export const { act, checkSuiteControls } = await import(pathToFileURL(join(directory, 'ui/uitest/controls.mjs')));
