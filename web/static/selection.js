// Bulk selection interactions for the file browser.
//
// - Click a file card to toggle it.
// - Drag (Photoshop-style marquee) over cards to select everything under the box.
// Selection state lives in Alpine.store('selection'); this file only mutates the
// store (which drives the checkboxes / card rings reactively).
(function () {
  var startX = 0, startY = 0, dragging = false, arming = false, rect = null;

  function container() {
    return document.getElementById('browser-content');
  }
  function store() {
    try { return Alpine.store('selection'); } catch (e) { return null; }
  }
  function onInteractive(el) {
    return !!el.closest('.ctx-menu, .ctx-submenu, .dropdown, .dropdown-menu, input, form, .selection-bar');
  }

  document.addEventListener('pointerdown', function (e) {
    var sel = store(), c = container();
    if (!sel || !sel.active || !c || !c.contains(e.target)) return;
    if (onInteractive(e.target)) return;
    var fileCard = e.target.closest('.card[data-file-id]');
    var folderCard = e.target.closest('.card[data-folder-id]');
    if (folderCard && !fileCard) return; // leave folder cards clickable (navigation)
    arming = true;
    dragging = false;
    startX = e.clientX;
    startY = e.clientY;
  });

  document.addEventListener('pointermove', function (e) {
    if (!arming) return;
    if (!dragging) {
      if (Math.abs(e.clientX - startX) < 5 && Math.abs(e.clientY - startY) < 5) return;
      dragging = true;
      rect = document.createElement('div');
      rect.className = 'marquee-rect';
      document.body.appendChild(rect);
    }
    rect.style.left = Math.min(startX, e.clientX) + 'px';
    rect.style.top = Math.min(startY, e.clientY) + 'px';
    rect.style.width = Math.abs(e.clientX - startX) + 'px';
    rect.style.height = Math.abs(e.clientY - startY) + 'px';
    applyMarquee();
  });

  function applyMarquee() {
    var sel = store(), c = container();
    if (!sel || !rect || !c) return;
    var rr = rect.getBoundingClientRect();
    var cards = c.querySelectorAll('.card[data-file-id]');
    for (var i = 0; i < cards.length; i++) {
      var card = cards[i];
      var cr = card.getBoundingClientRect();
      var overlaps = !(cr.right < rr.left || cr.left > rr.right || cr.bottom < rr.top || cr.top > rr.bottom);
      if (overlaps && sel.ids.indexOf(card.dataset.fileId) === -1) {
        sel.ids = sel.ids.concat(card.dataset.fileId);
      }
    }
  }

  document.addEventListener('pointerup', function (e) {
    if (!arming) return;
    arming = false;
    if (rect) {
      rect.remove();
      rect = null;
    }
    if (!dragging) {
      var sel = store();
      var card = e.target.closest('.card[data-file-id]');
      if (card && sel) {
        var id = card.dataset.fileId;
        sel.ids = sel.ids.indexOf(id) !== -1
          ? sel.ids.filter(function (x) { return x !== id })
          : sel.ids.concat(id);
      }
    }
    dragging = false;
  });
})();
