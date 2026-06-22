// PM tab — editable Minutes-of-Meeting form helpers (no framework, no build).

// Add a blank discussion / follow-up row, naming its fields so they post as the
// right parallel arrays (discussion_* or followup_*).
function momAdd(kind) {
  var list = document.getElementById(kind === 'disc' ? 'disc-list' : 'follow-list');
  var tmpl = document.getElementById('mom-row-tmpl');
  if (!list || !tmpl) return;
  var row = tmpl.content.firstElementChild.cloneNode(true);
  var prefix = kind === 'disc' ? 'discussion' : 'followup';
  row.querySelector('input').name = prefix + '_title';
  row.querySelector('textarea').name = prefix + '_desc';
  list.appendChild(row);
  row.querySelector('input').focus();
}

// Remove a row.
function momDel(btn) {
  var row = btn.closest('.mom-row');
  if (row) row.remove();
}

// Copy the current (edited) minutes as plain text.
function momCopy() {
  var f = document.getElementById('mom-form');
  if (!f) return;
  var v = function (n) { var el = f.elements[n]; return el ? el.value.trim() : ''; };
  var lines = ['MINUTES OF MEETING', ''];
  if (v('date_time')) lines.push('Date / Time: ' + v('date_time'));
  if (v('location')) lines.push('Location: ' + v('location'));
  if (v('purpose')) lines.push('Purpose: ' + v('purpose'));
  if (v('attendees')) lines.push('Attendees:\n' + v('attendees'));
  lines.push('', 'Meeting Discussions and Info:');
  lines.push.apply(lines, rowsText('disc-list'));
  lines.push('', 'Follow Up:');
  lines.push.apply(lines, rowsText('follow-list'));
  if (v('prepared_by')) lines.push('', 'Prepared by: ' + v('prepared_by'));

  navigator.clipboard.writeText(lines.join('\n')).then(function () {
    var tag = document.getElementById('mom-copied');
    if (tag) { tag.style.display = 'inline'; setTimeout(function () { tag.style.display = 'none'; }, 2000); }
  });
}

function rowsText(listId) {
  var out = [], n = 1;
  document.querySelectorAll('#' + listId + ' .mom-row').forEach(function (row) {
    var t = row.querySelector('input').value.trim();
    var d = row.querySelector('textarea').value.trim();
    if (!t && !d) return;
    out.push(n + '. ' + (t ? t + ' — ' : '') + d);
    n++;
  });
  return out;
}
