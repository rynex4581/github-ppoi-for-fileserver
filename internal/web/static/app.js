// 差分の行コメント。行の「+」を押すと、その行の下にコメントの入力欄を出す。
// 使う場所はこの1点だけで、送信は通常のフォーム送信で行う。
(function () {
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
})();
