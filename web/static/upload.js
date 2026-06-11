function initUppy(targetFolderId) {
  if (typeof Uppy === 'undefined') return;

  var existing = window.__vaulxUppy;
  if (existing) { existing.destroy(); }

  var uppy = new Uppy.Uppy({
    autoProceed: false,
    restrictions: { maxFileSize: null },
  });

  uppy.use(Uppy.Dashboard, {
    inline: true,
    target: '#uppy-container',
    proudlyDisplayPoweredByUppy: false,
    height: 320,
  });

  uppy.use(Uppy.AwsS3Multipart, {
    shouldUseMultipart: function(file) { return file.size > 100 * 1024 * 1024; },

    createMultipartUpload: async function(file) {
      var res = await fetch('/api/s3/multipart', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          filename: file.name,
          contentType: file.type || 'application/octet-stream',
          folderId: targetFolderId || '',
        }),
      });
      if (!res.ok) throw new Error('createMultipartUpload failed');
      var data = await res.json();
      uppy.setFileMeta(file.id, { fileId: data.fileId });
      return data;
    },

    listParts: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key;
      var res = await fetch('/api/s3/multipart/' + uploadId + '?key=' + encodeURIComponent(key));
      if (!res.ok) throw new Error('listParts failed');
      return res.json();
    },

    signPart: async function(file, _ref) {
      var uploadId = _ref.uploadId, partNumber = _ref.partNumber, key = _ref.key;
      var res = await fetch('/api/s3/multipart/' + uploadId + '/' + partNumber + '?key=' + encodeURIComponent(key));
      if (!res.ok) throw new Error('signPart failed');
      var data = await res.json();
      return { url: data.url };
    },

    abortMultipartUpload: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key;
      await fetch('/api/s3/multipart/' + uploadId + '?key=' + encodeURIComponent(key), { method: 'DELETE' });
    },

    completeMultipartUpload: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key, parts = _ref.parts;
      var fileId = (file.meta && file.meta.fileId) || '';
      var res = await fetch('/api/s3/multipart/' + uploadId + '/complete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key: key, fileId: fileId, parts: parts }),
      });
      if (!res.ok) throw new Error('completeMultipartUpload failed');
      return res.json();
    },
  });

  uppy.on('complete', function(result) {
    if (result.successful && result.successful.length > 0) {
      var target = window.location.pathname;
      if (typeof htmx !== 'undefined') {
        htmx.ajax('GET', target, { target: '#browser-content', swap: 'outerHTML' });
      }
    }
  });

  window.__vaulxUppy = uppy;
}
