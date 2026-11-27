# Third-party software in acat releases

acat itself is licensed under the Apache License 2.0 (see `LICENSE`).
Release bundles also contain the following third-party components.

## Azure Communication Services Calling SDK (`@azure/communication-calling`)

The video helper (`acat-video`) contains the object code of the Azure
Communication Services Calling SDK. It is **not** open source; it is
distributed under the *Microsoft Software License Terms for the Azure
Communication Services SDK*. By installing or using acat's video helper you
agree to those terms, which are available at
<https://www.npmjs.com/package/@azure/communication-calling> (LICENSE file
of the package). The SDK's own notices are kept in the bundle
(`call.bundle.js.LEGAL.txt`). The source of acat never contains the SDK; it
is fetched from npm at build time.

If you do not accept these terms, do not install the video helper: acat's
chat works without it (calls are then disabled).

## Electron

The video helper runs on Electron (MIT) and Chromium (BSD-3-Clause and
others). Their license texts ship inside the helper (`LICENSE`,
`LICENSES.chromium.html`).

## Go modules

The acat client and relay are built from Go modules under permissive
licenses (BSD-3-Clause, MIT, Apache-2.0). The release's SBOM
(`*.spdx.json`) lists every module and version.
