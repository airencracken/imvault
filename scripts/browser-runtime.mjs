// SPDX-License-Identifier: AGPL-3.0-or-later
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
const directory = execFileSync('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/airencracken/comfylib'], { env: { ...process.env, GOWORK: 'off' }, encoding: 'utf8' }).trim();
const runtime = await import(pathToFileURL(directory + '/ui/uitest/runtime.mjs').href);
export const { findBrowser, temporaryDirectory, reservePort, waitForHTTP, stopProcess, spawnServer, checkOwnerMetrics } = runtime;
