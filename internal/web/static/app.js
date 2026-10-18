// 画面で使う JS はこのファイルだけ（インラインのスクリプトは CSP で禁じている）。
// 1. 差分の行コメント：行の「+」を押すと、その行の下にコメントの入力欄を出す
// 2. 添付ファイル：本文の入力欄に画像を Ctrl+V で貼ると、本文に ![image](attachment:N) の印を入れ、
//    画像はフォームの送信時に一緒に送る。保存と印の置き換えはサーバ側で行う
// 3. Markdown のプレビュー：本文の入力欄（textarea.app-textarea）の上に「書き込み／プレビュー」の
//    タブを付け、プレビューはサーバの POST /markdown で描画したものを出す
// 4. 細かいもの：クリックで全選択する入力欄（.app-select-on-click）、送信前の確認（form の data-confirm）
(function () {
  // ---- Markdown のプレビュー ----
  function setupPreview(textarea) {
    if (textarea._preview || textarea.hasAttribute('data-no-preview')) return;
    textarea._preview = true;
    var labels = document.body.dataset;
    var tabs = document.createElement('div');
    tabs.className = 'tabnav app-md-tabs mb-2';
    var nav = document.createElement('nav');
    nav.className = 'tabnav-tabs';
    [['write', labels.lWrite || 'Write'], ['preview', labels.lPreview || 'Preview']].forEach(function (t, i) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'tabnav-tab' + (i === 0 ? ' selected' : '');
      b.dataset.tab = t[0];
      b.textContent = t[1];
      nav.appendChild(b);
    });
    tabs.appendChild(nav);
    var preview = document.createElement('div');
    preview.className = 'markdown-body app-md-preview border rounded-2 p-3';
    preview.hidden = true;
    textarea.parentNode.insertBefore(tabs, textarea);
    textarea.parentNode.insertBefore(preview, textarea.nextSibling);
    nav.addEventListener('click', function (e) {
      var btn = e.target.closest('.tabnav-tab');
      if (!btn) return;
      nav.querySelectorAll('.tabnav-tab').forEach(function (b) { b.classList.toggle('selected', b === btn); });
      var show = btn.dataset.tab === 'preview';
      textarea.hidden = show;
      preview.hidden = !show;
      if (!show) return;
      preview.textContent = labels.lLoading || '...';
      var body = new URLSearchParams();
      body.set('text', textarea.value);
      fetch('/markdown', { method: 'POST', body: body, headers: { 'Content-Type': 'application/x-www-form-urlencoded' } })
        .then(function (r) { return r.text(); })
        .then(function (html) { preview.innerHTML = html; })
        .catch(function () { preview.textContent = 'error'; });
    });
  }
  function setupAllPreviews(root) {
    (root.querySelectorAll ? root.querySelectorAll('textarea.app-textarea') : []).forEach(setupPreview);
  }
  setupAllPreviews(document);
  new MutationObserver(function (list) {
    list.forEach(function (m) {
      m.addedNodes.forEach(function (n) { if (n.nodeType === 1) setupAllPreviews(n); });
    });
  }).observe(document.body, { childList: true, subtree: true });

  document.addEventListener('click', function (e) {
    var sel = e.target.closest('.app-select-on-click');
    if (sel) sel.select();
  });
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (form.dataset && form.dataset.confirm && !confirm(form.dataset.confirm)) e.preventDefault();
  });

  // ---- 行コメント ----
  function closeForm(row) {
    var next = row.nextElementSibling;
    if (next && next.classList.contains('app-inline-form')) {
      next.remove();
      return true;
    }
    return false;
  }

  document.addEventListener('click', function (e) {
    var cancel = e.target.closest('.app-cancel-comment');
    if (cancel) {
      var formRow = cancel.closest('tr.app-inline-form');
      if (formRow) formRow.remove();
      return;
    }
    var btn = e.target.closest('.app-add-comment');
    if (!btn) return;
    var row = btn.closest('tr');
    if (!row || closeForm(row)) return;
    var tpl = document.getElementById('app-line-comment-form');
    if (!tpl) return;
    var tr = document.createElement('tr');
    tr.className = 'app-inline-form';
    var td = document.createElement('td');
    td.colSpan = 3;
    td.appendChild(tpl.content.cloneNode(true));
    td.querySelector('[name=path]').value = btn.dataset.path;
    td.querySelector('[name=side]').value = btn.dataset.side;
    td.querySelector('[name=line]').value = btn.dataset.line;
    tr.appendChild(td);
    row.after(tr);
    td.querySelector('textarea').focus();
  });

  // ---- 添付ファイル（Ctrl+V） ----
  function fileInputFor(form) {
    var input = form.querySelector('input[type=file][name=attachments]');
    if (!input) {
      input = document.createElement('input');
      input.type = 'file';
      input.name = 'attachments';
      input.multiple = true;
      input.hidden = true;
      form.appendChild(input);
      form.enctype = 'multipart/form-data';
    }
    if (!form._attachments) form._attachments = new DataTransfer();
    return input;
  }

  function insertAtCursor(textarea, text) {
    var start = textarea.selectionStart, end = textarea.selectionEnd, v = textarea.value;
    var before = v.slice(0, start), after = v.slice(end);
    if (before && !before.endsWith('\n')) text = '\n' + text;
    if (after && !after.startsWith('\n')) text = text + '\n';
    textarea.value = before + text + after;
    var pos = before.length + text.length;
    textarea.setSelectionRange(pos, pos);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  }

  document.addEventListener('paste', function (e) {
    var textarea = e.target.closest('textarea.app-textarea');
    // マージメッセージの欄などは添付を受けない（コミットメッセージに印だけ残るため）
    if (textarea && textarea.hasAttribute('data-no-attach')) return;
    if (!textarea || !e.clipboardData) return;
    var form = textarea.closest('form');
    if (!form) return;
    var items = e.clipboardData.items || [];
    var files = [];
    for (var i = 0; i < items.length; i++) {
      if (items[i].kind === 'file' && items[i].type.indexOf('image/') === 0) {
        var f = items[i].getAsFile();
        if (f) files.push(f);
      }
    }
    if (files.length === 0) return;
    e.preventDefault();
    var input = fileInputFor(form);
    var limit = Number(textarea.dataset.maxBytes || 0);
    files.forEach(function (f) {
      if (limit && f.size > limit) {
        alert(textarea.dataset.tooLarge || 'too large');
        return;
      }
      var ext = (f.type.split('/')[1] || 'png').replace('jpeg', 'jpg');
      var n = form._attachments.files.length + 1;
      var named = new File([f], 'image' + n + '.' + ext, { type: f.type });
      form._attachments.items.add(named);
      input.files = form._attachments.files;
      insertAtCursor(textarea, '![image](attachment:' + n + ')');
    });
  });
})();
