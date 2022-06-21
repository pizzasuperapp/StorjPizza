// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

// eslint-disable-next-line no-undef
importScripts('/static/static/wasm/wasm_exec.js');

if (!WebAssembly.instantiate) {
    self.postMessage(new Error('web assembly is not supported'));
}

self.onmessage = async function (event) {
    const data = event.data;
    let result;
    let apiKey;
    switch (data.type) {
    case 'Setup':
        try {
            const go = new global.Go();
            const response = await fetch('/static/static/wasm/access.wasm')
            const buffer = await response.arrayBuffer();
            const module = await WebAssembly.compile(buffer);
            const instance = await WebAssembly.instantiate(module, go.importObject);

            go.run(instance)

            self.postMessage('configured');
        } catch (e) {
            self.postMessage(new Error(e.message))
        }

        break;
    case 'DeriveAndEncryptRootKey':
        {
            const passphrase = data.passphrase;
            const projectID = data.projectID;
            const aesKey = data.aesKey;

            result = self.deriveAndEncryptRootKey(passphrase, projectID, aesKey);
            self.postMessage(result);
        }
        break;
    case 'GenerateAccess':
        {
            apiKey = data.apiKey;
            const passphrase = data.passphrase;
            const projectID = data.projectID;
            const nodeURL = data.satelliteNodeURL;

            result = self.generateAccessGrant(nodeURL, apiKey, passphrase, projectID);

            self.postMessage(result);
        }
        break;
    case 'SetPermission': // fallthrough
    case 'RestrictGrant':
        {
            const isDownload = data.isDownload;
            const isUpload = data.isUpload;
            const isList = data.isList;
            const isDelete = data.isDelete;
            const notBefore = data.notBefore;
            const notAfter = data.notAfter;

            let permission = self.newPermission().value;

            permission.AllowDownload = isDownload;
            permission.AllowUpload = isUpload;
            permission.AllowDelete = isDelete;
            permission.AllowList = isList;

            if (notBefore) permission.NotBefore = notBefore;
            if (notAfter) permission.NotAfter = notAfter;

            if (data.type === "SetPermission") {
                const buckets = data.buckets;
                apiKey = data.apiKey;
                result = self.setAPIKeyPermission(apiKey, buckets, permission);
            } else {
                const paths = data.paths;
                const accessGrant = data.grant;
                result = self.restrictGrant(accessGrant, paths, permission);
            }

            self.postMessage(result);
        }
        break;
    default:
        self.postMessage(new Error('provided message event type is not supported'));
    }
};