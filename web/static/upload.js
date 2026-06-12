function initUppy(targetFolderId) {
  if (typeof Uppy === 'undefined') {
    console.error('[vaulx-uppy] Uppy is not loaded — check CDN script tag');
    return;
  }

  console.log('[vaulx-uppy] initUppy called', { targetFolderId: targetFolderId, origin: window.location.origin });

  var existing = window.__vaulxUppy;
  if (existing) {
    console.log('[vaulx-uppy] destroying existing Uppy instance');
    existing.destroy();
  }

  var uppy = new Uppy.Uppy({
    autoProceed: false,
    restrictions: { maxFileSize: null },
    logger: {
      debug: function() { console.debug('[uppy]', ...arguments); },
      warn:  function() { console.warn('[uppy]',  ...arguments); },
      error: function() { console.error('[uppy]', ...arguments); },
    },
  });

  uppy.use(Uppy.Dashboard, {
    inline: true,
    target: '#uppy-container',
    proudlyDisplayPoweredByUppy: false,
    height: 320,
  });

  uppy.use(Uppy.AwsS3, {
    companionUrl: window.location.origin,
    shouldUseMultipart: function(file) {
      console.log('[vaulx-uppy] shouldUseMultipart →', true, 'file:', file.name, file.size, 'bytes');
      return true;
    },

    createMultipartUpload: async function(file) {
      var body = {
        filename: file.name,
        contentType: file.type || 'application/octet-stream',
        folderId: targetFolderId || '',
      };
      console.log('[vaulx-uppy] createMultipartUpload → POST /api/s3/multipart', body);
      var res = await fetch('/api/s3/multipart', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      console.log('[vaulx-uppy] createMultipartUpload response status:', res.status, res.statusText);
      if (!res.ok) {
        var errText = await res.text();
        console.error('[vaulx-uppy] createMultipartUpload FAILED — status:', res.status, 'body:', errText);
        throw new Error('createMultipartUpload failed: ' + res.status + ' ' + errText);
      }
      var data = await res.json();
      console.log('[vaulx-uppy] createMultipartUpload success:', data);
      uppy.setFileMeta(file.id, { fileId: data.fileId });
      return data;
    },

    listParts: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key;
      var url = '/api/s3/multipart/' + uploadId + '?key=' + encodeURIComponent(key);
      console.log('[vaulx-uppy] listParts → GET', url);
      var res = await fetch(url);
      console.log('[vaulx-uppy] listParts response status:', res.status, res.statusText);
      if (!res.ok) {
        var errText = await res.text();
        console.error('[vaulx-uppy] listParts FAILED — status:', res.status, 'body:', errText);
        throw new Error('listParts failed: ' + res.status + ' ' + errText);
      }
      var data = await res.json();
      console.log('[vaulx-uppy] listParts success, parts count:', Array.isArray(data) ? data.length : data);
      return data;
    },

    signPart: async function(file, _ref) {
      var uploadId = _ref.uploadId, partNumber = _ref.partNumber, key = _ref.key;
      var url = '/api/s3/multipart/' + uploadId + '/' + partNumber + '?key=' + encodeURIComponent(key);
      console.log('[vaulx-uppy] signPart → GET', url, '(part', partNumber, ')');
      var res = await fetch(url);
      console.log('[vaulx-uppy] signPart response status:', res.status, res.statusText);
      if (!res.ok) {
        var errText = await res.text();
        console.error('[vaulx-uppy] signPart FAILED — status:', res.status, 'body:', errText);
        throw new Error('signPart failed: ' + res.status + ' ' + errText);
      }
      var data = await res.json();
      console.log('[vaulx-uppy] signPart success, presigned URL domain:', data.url ? new URL(data.url).hostname : 'missing');
      if (!data.url) {
        console.error('[vaulx-uppy] signPart: response has no .url field', data);
        throw new Error('signPart: missing url in response');
      }
      return { url: data.url };
    },

    abortMultipartUpload: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key;
      var url = '/api/s3/multipart/' + uploadId + '?key=' + encodeURIComponent(key);
      console.log('[vaulx-uppy] abortMultipartUpload → DELETE', url);
      var res = await fetch(url, { method: 'DELETE' });
      console.log('[vaulx-uppy] abortMultipartUpload response status:', res.status, res.statusText);
    },

    completeMultipartUpload: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key, parts = _ref.parts;
      var fileId = (file.meta && file.meta.fileId) || '';
      var payload = { key: key, fileId: fileId, parts: parts };
      console.log('[vaulx-uppy] completeMultipartUpload → POST /api/s3/multipart/' + uploadId + '/complete', payload);
      var res = await fetch('/api/s3/multipart/' + uploadId + '/complete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      console.log('[vaulx-uppy] completeMultipartUpload response status:', res.status, res.statusText);
      if (!res.ok) {
        var errText = await res.text();
        console.error('[vaulx-uppy] completeMultipartUpload FAILED — status:', res.status, 'body:', errText);
        throw new Error('completeMultipartUpload failed: ' + res.status + ' ' + errText);
      }
      var data = await res.json();
      console.log('[vaulx-uppy] completeMultipartUpload success:', data);
      return data;
    },
  });

  // Lifecycle events for full visibility
  uppy.on('file-added', function(file) {
    console.log('[vaulx-uppy] file-added:', file.name, file.size, 'bytes', file.type);
  });
  uppy.on('upload', function(data) {
    console.log('[vaulx-uppy] upload started, fileIDs:', data.fileIDs);
  });
  uppy.on('upload-progress', function(file, progress) {
    console.log('[vaulx-uppy] progress', file.name, progress.bytesUploaded, '/', progress.bytesTotal);
  });
  uppy.on('upload-success', function(file, response) {
    console.log('[vaulx-uppy] upload-success:', file.name, response);
  });
  uppy.on('upload-error', function(file, error, response) {
    console.error('[vaulx-uppy] upload-error:', file.name, error.message);
    if (response) console.error('[vaulx-uppy] error response:', response.status, response.body);
  });
  uppy.on('error', function(error) {
    console.error('[vaulx-uppy] global error:', error.message, error.stack);
  });
  uppy.on('complete', function(result) {
    console.log('[vaulx-uppy] complete — successful:', result.successful.length, 'failed:', result.failed.length);
    if (result.failed.length > 0) {
      result.failed.forEach(function(f) {
        console.error('[vaulx-uppy] failed file:', f.name, f.error);
      });
    }
    if (result.successful && result.successful.length > 0) {
      var target = window.location.pathname;
      console.log('[vaulx-uppy] refreshing browser content via htmx, target:', target);
      if (typeof htmx !== 'undefined') {
        htmx.ajax('GET', target, { target: '#browser-content', swap: 'outerHTML' });
      } else {
        console.warn('[vaulx-uppy] htmx not available, reloading page');
        window.location.reload();
      }
    }
  });

  window.__vaulxUppy = uppy;
  console.log('[vaulx-uppy] Uppy ready');
}
