(function () {
  'use strict';

  var token = (document.querySelector('meta[name="csrf-token"]') || {}).content || '';
  var state = {
    tab: 'list',
    step: 1,
    editId: null,
    appnameTouched: false,
    mode: 'reverse',
    volumesLoaded: false,
    polling: null,
    lastPackage: null
  };

  function $(id) { return document.getElementById(id); }
  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function msgOf(err) {
    if (!err) return '未知错误';
    return err.message || String(err);
  }

  function apiURL(path) {
    var m = location.pathname.match(/^(.*\/index\.cgi)(?:\/.*)?$/);
    return m ? m[1] + path : path;
  }

  function api(path, opts) {
    opts = opts || {};
    var headers = { 'Accept': 'application/json' };
    if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
    if (opts.method && opts.method !== 'GET') headers['X-CSRF-Token'] = token;
    var init = { method: opts.method || 'GET', headers: headers, credentials: 'same-origin' };
    if (opts.body !== undefined) init.body = JSON.stringify(opts.body);
    return fetch(apiURL(path), init).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { data = null; }
        if (!res.ok) {
          var m = data && data.error ? data.error.message : (text || ('HTTP ' + res.status));
          var err = new Error(m);
          err.code = data && data.error ? data.error.code : 'http_' + res.status;
          throw err;
        }
        return data;
      });
    });
  }

  function toast(text, isErr) {
    var wrap = $('toastWrap');
    var div = document.createElement('div');
    div.className = 'toast' + (isErr ? ' err' : '');
    div.textContent = text;
    wrap.appendChild(div);
    setTimeout(function () { div.remove(); }, 4200);
  }

  function statusInfo(s) {
    var map = {
      draft: ['草稿', 'neutral'],
      building: ['生成中', 'info'],
      installing: ['安装中', 'info'],
      starting: ['启动中', 'warn'],
      running: ['运行中', 'ok'],
      installed_not_running: ['已安装未运行', 'warn'],
      uninstalling: ['卸载中', 'warn'],
      failed: ['失败', 'bad'],
      broken: ['异常', 'bad'],
      missing: ['已丢失', 'bad'],
      uninstall_failed: ['卸载失败', 'bad'],
      unmanaged: ['未纳管', 'neutral']
    };
    var v = map[s] || [s || '未知', 'neutral'];
    return { text: v[0], cls: v[1] };
  }

  function entryURL(appname) {
    return location.origin + '/cgi/ThirdParty/' + encodeURIComponent(appname) + '/index.cgi/';
  }

  function fmtTime(s) {
    if (!s) return '';
    var d = new Date(s);
    if (isNaN(d.getTime())) return s;
    var p = function (n) { return String(n).padStart(2, '0'); };
    return (d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  function loadHealth() {
    api('/api/health').then(function (h) {
      var pill = $('cliPill'), text = $('cliText');
      if (h.cli_ok) {
        pill.className = 'pill ok';
        text.textContent = 'appcenter-cli 就绪';
      } else if (h.cli_path) {
        pill.className = 'pill warn';
        text.textContent = 'CLI 存在但无法调用';
      } else {
        pill.className = 'pill bad';
        text.textContent = '未找到 appcenter-cli';
      }
      pill.title = h.cli_path || '需要 root 运行托管台';
    }).catch(function (e) {
      $('cliPill').className = 'pill bad';
      $('cliText').textContent = '环境检查失败';
    });
  }

  function loadList() {
    return api('/api/targets').then(function (data) {
      renderList(data);
    }).catch(function (e) {
      $('listBody').innerHTML = '<div class="empty"><div class="big">加载失败</div><div>' + esc(msgOf(e)) + '</div></div>';
    });
  }

  function renderList(data) {
    var targets = data.targets || [];
    if (!targets.length) {
      $('listBody').innerHTML = '<div class="empty"><div class="big">还没有代理应用</div><div>点击右上角“新建代理”，填一个 ip:port 就能生成安装包。</div><div style="margin-top:16px"><button class="btn primary" id="emptyNew" type="button">新建第一个代理</button></div></div>';
      var b = $('emptyNew');
      if (b) b.addEventListener('click', function () { startNew(); });
    } else {
      var rows = targets.map(function (t) {
        var sp = t.spec, rt = t.runtime || {};
        var st = statusInfo(rt.state);
        var op = rt.last_operation;
        var opText = '';
        if (op) {
          var when = fmtTime(op.started_at);
          var result = op.status === 'succeeded' ? '成功' : (op.status === 'running' ? '进行中' : '失败');
          opText = when + ' · ' + result;
          if (op.error) opText += ' · ' + op.error;
        } else {
          opText = '—';
        }
        var url = entryURL(sp.appname);
        var actions = [];
        if (rt.state === 'running' || rt.state === 'installed_not_running') {
          actions.push('<button class="btn sm" data-act="open" data-id="' + esc(sp.id) + '" type="button">打开</button>');
        }
        actions.push('<button class="btn sm" data-act="edit" data-id="' + esc(sp.id) + '" type="button">编辑</button>');
        actions.push('<button class="btn sm" data-act="apply" data-id="' + esc(sp.id) + '" type="button">' + (rt.state === 'draft' ? '安装' : '重新应用') + '</button>');
        if (rt.last_known_good_path) actions.push('<button class="btn sm" data-act="rollback" data-id="' + esc(sp.id) + '" type="button">回滚</button>');
        if (rt.package_path) actions.push('<a class="btn sm" href="/api/targets/' + encodeURIComponent(sp.id) + '/package">下载</a>');
        actions.push('<button class="btn sm" data-act="log" data-id="' + esc(sp.id) + '" type="button">日志</button>');
        actions.push('<button class="btn sm danger" data-act="delete" data-id="' + esc(sp.id) + '" type="button">删除</button>');
        return '<tr>' +
          '<td class="app-cell"><div class="name">' + esc(sp.display_name) + '</div><div class="id mono">' + esc(sp.appname) + '</div></td>' +
          '<td class="target-cell"><div class="mono">' + esc(sp.target_url) + '</div><div class="faint">' + (sp.mode === 'direct' ? '直连 302' : '反向代理') + '</div></td>' +
          '<td><span class="pill ' + st.cls + '"><span class="dot"></span>' + st.text + '</span>' + (rt.needs_reapply ? '<div class="faint" style="font-size:11px;margin-top:3px">待重新应用</div>' : '') + '</td>' +
          '<td class="nowrap"><a class="mono" style="font-size:11.5px" href="' + esc(url) + '" target="_blank" rel="noopener">' + esc('…/index.cgi/') + '</a></td>' +
          '<td class="muted" style="font-size:12px;max-width:230px">' + esc(opText) + '</td>' +
          '<td><div class="row-actions">' + actions.join('') + '</div></td>' +
          '</tr>';
      }).join('');
      $('listBody').innerHTML = '<div style="overflow-x:auto"><table><thead><tr><th>应用</th><th>目标</th><th>状态</th><th>入口</th><th>最近操作</th><th style="text-align:right">操作</th></tr></thead><tbody>' + rows + '</tbody></table></div>';
    }

    var un = data.unmanaged || [];
    if (un.length) {
      var urows = un.map(function (u) {
        return '<tr><td class="mono">' + esc(u.appname) + '</td><td>' + esc(u.display_name) + '</td><td>' + esc(u.version) + '</td><td>' + esc(u.status) + '</td></tr>';
      }).join('');
      $('unmanagedWrap').innerHTML = '<div class="panel"><div class="panel-head"><div><h2>系统里已有、但不在托管台的应用</h2><p>只读展示；要接管请用相同 appname 新建，或先在应用中心卸载。</p></div></div><table><thead><tr><th>appname</th><th>名称</th><th>版本</th><th>状态</th></tr></thead><tbody>' + urows + '</tbody></table></div>';
    } else {
      $('unmanagedWrap').innerHTML = '';
    }
  }

  function findTarget(id) {
    var found = null;
    if (window.__lastTargets) {
      window.__lastTargets.forEach(function (t) { if (t.spec.id === id) found = t; });
    }
    return found;
  }

  function loadListCached() {
    return api('/api/targets').then(function (data) {
      window.__lastTargets = data.targets || [];
      renderList(data);
      return data;
    });
  }

  function onListClick(e) {
    var btn = e.target.closest('button[data-act]');
    if (!btn) return;
    var id = btn.getAttribute('data-id');
    var act = btn.getAttribute('data-act');
    if (act === 'open') {
      var t = findTarget(id);
      if (t) window.open(entryURL(t.spec.appname), '_blank', 'noopener');
    } else if (act === 'edit') {
      var t2 = findTarget(id);
      if (t2) startEdit(t2);
    } else if (act === 'apply') {
      if (!confirm('将重新生成安装包并安装。已安装的同名应用会先卸载，继续？')) return;
      applyTarget(id, '');
    } else if (act === 'rollback') {
      if (!confirm('用上一版安装包重装，继续？')) return;
      postOp('/api/targets/' + id + '/rollback').catch(function (err) { toast(msgOf(err), true); });
    } else if (act === 'delete') {
      if (!confirm('卸载应用并删除托管记录？安装包也会删除。')) return;
      postOp('/api/targets/' + id + '/delete').then(function () {
        setTimeout(function () { loadListCached(); }, 800);
      }).catch(function (err) { toast(msgOf(err), true); });
    } else if (act === 'log') {
      showLog(id);
    }
  }

  function postOp(path) {
    return api(path, { method: 'POST', body: {} }).then(function (r) {
      toast('操作已开始');
      return r;
    });
  }

  function applyTarget(id, kind) {
    api('/api/targets/' + id + '/apply', { method: 'POST', body: { kind: kind || '' } }).then(function (r) {
      switchTab('wizard');
      showStep(4);
      showProgress(r.operation_id);
    }).catch(function (err) {
      toast(msgOf(err), true);
    });
  }

  function showLog(id) {
    api('/api/targets/' + id + '/log?lines=300').then(function (d) {
      openModal('最近日志', '<pre class="log">' + esc((d.lines || []).join('\n')) + '</pre>', []);
    }).catch(function (err) { toast(msgOf(err), true); });
  }

  function openModal(title, bodyHTML, buttons) {
    var root = $('modalRoot');
    var foot = (buttons || []).map(function (b, i) {
      return '<button class="btn ' + (b.cls || '') + '" data-mi="' + i + '" type="button">' + esc(b.label) + '</button>';
    }).join('');
    root.innerHTML = '<div class="modal-back"><div class="modal"><div class="modal-head"><h3>' + esc(title) + '</h3><button class="btn sm ghost" data-close="1" type="button">关闭</button></div><div class="modal-body">' + bodyHTML + '</div>' + (foot ? '<div class="modal-foot">' + foot + '</div>' : '') + '</div></div>';
    root.querySelector('[data-close]').addEventListener('click', closeModal);
    root.querySelector('.modal-back').addEventListener('click', function (e) { if (e.target === this) closeModal(); });
    (buttons || []).forEach(function (b, i) {
      var el = root.querySelector('[data-mi="' + i + '"]');
      if (el) el.addEventListener('click', function () { b.onClick(closeModal); });
    });
  }
  function closeModal() { $('modalRoot').innerHTML = ''; }

  function switchTab(name) {
    state.tab = name;
    $('tabList').classList.toggle('active', name === 'list');
    $('tabWizard').classList.toggle('active', name === 'wizard');
    $('listView').style.display = name === 'list' ? '' : 'none';
    $('wizardView').style.display = name === 'wizard' ? '' : 'none';
  }

  function resetForm() {
    state.editId = null;
    state.appnameTouched = false;
    state.mode = 'reverse';
    state.lastPackage = null;
    $('target_url').value = '';
    $('display_name').value = '';
    $('appname').value = '';
    $('appname').disabled = false;
    $('description').value = '';
    $('version').value = '1.0.0';
    $('entry_title').value = '';
    $('ui_type').value = 'url';
    $('all_users').checked = false;
    $('no_display').checked = false;
    $('skip_tls_verify').checked = false;
    $('allow_loopback').checked = false;
    $('auth_on').checked = false;
    $('authFields').style.display = 'none';
    $('auth_user').value = '';
    $('auth_pass').value = '';
    $('targetError').textContent = '';
    $('testResult').innerHTML = '';
    $('applyBox').style.display = 'none';
    $('applyAlert').innerHTML = '';
    $('applyLinks').innerHTML = '';
    setMode('reverse');
    if (!state.volumesLoaded) loadVolumes();
    showStep(1);
  }

  function startNew() {
    switchTab('wizard');
    resetForm();
  }

  function startEdit(t) {
    switchTab('wizard');
    resetForm();
    state.editId = t.spec.id;
    state.appnameTouched = true;
    $('target_url').value = t.spec.target_url;
    $('display_name').value = t.spec.display_name;
    $('appname').value = t.spec.appname;
    $('appname').disabled = true;
    $('description').value = t.spec.description || '';
    $('version').value = t.spec.version || '1.0.0';
    $('entry_title').value = t.spec.entry_title || '';
    $('ui_type').value = t.spec.ui_type || 'url';
    $('install_volume').value = t.spec.install_volume || '1';
    $('all_users').checked = !!t.spec.all_users;
    $('no_display').checked = !!t.spec.no_display;
    $('skip_tls_verify').checked = !!t.spec.skip_tls_verify;
    $('allow_loopback').checked = !!t.spec.allow_loopback;
    if (t.spec.basic_auth) {
      $('auth_on').checked = true;
      $('auth_user').value = t.spec.basic_auth.username || '';
      $('authFields').style.display = '';
    }
    setMode(t.spec.mode || 'reverse');
    showStep(2);
  }

  function showStep(n) {
    state.step = n;
    for (var i = 1; i <= 4; i++) {
      var sec = $('wzStep' + i);
      if (sec) sec.style.display = i === n ? '' : 'none';
    }
    var steps = document.querySelectorAll('#stepRail .step');
    for (var j = 0; j < steps.length; j++) {
      var k = Number(steps[j].getAttribute('data-step'));
      steps[j].classList.toggle('active', k === n);
      steps[j].classList.toggle('done', k < n);
    }
    $('backBtn').style.visibility = n > 1 ? 'visible' : 'hidden';
    $('nextBtn').textContent = n === 4 ? '生成并安装' : '下一步';
    if (n === 4) renderSummary();
  }

  function setMode(m) {
    state.mode = m;
    var cards = document.querySelectorAll('.mode-card');
    for (var i = 0; i < cards.length; i++) {
      cards[i].classList.toggle('sel', cards[i].getAttribute('data-mode') === m);
    }
    var reverse = m === 'reverse';
    $('reverseOnly1').style.display = reverse ? '' : 'none';
    $('reverseOnly2').style.display = reverse ? '' : 'none';
    if (!reverse) {
      $('auth_on').checked = false;
      $('authFields').style.display = 'none';
    }
  }

  function loadVolumes() {
    api('/api/volumes').then(function (d) {
      var sel = $('install_volume');
      sel.innerHTML = '';
      (d.volumes || [d.default || '1']).forEach(function (v) {
        var o = document.createElement('option');
        o.value = v;
        o.textContent = '卷 ' + v + (v === d.default ? '（默认）' : '');
        sel.appendChild(o);
      });
      sel.value = d.default || '1';
      state.volumesLoaded = true;
    }).catch(function () {});
  }

  function randHex4() {
    var a = new Uint8Array(2);
    crypto.getRandomValues(a);
    return Array.prototype.map.call(a, function (b) { return b.toString(16).padStart(2, '0'); }).join('');
  }

  function slugify(s) {
    return String(s || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 18);
  }

  function suggestAppName() {
    if (state.appnameTouched || state.editId) return;
    var base = slugify($('display_name').value);
    var name = base ? 'proxy.' + base : 'proxy';
    $('appname').value = name.slice(0, 26) + '.' + randHex4();
  }

  function targetError() {
    var raw = $('target_url').value.trim();
    if (!raw) return '请填写目标地址';
    var withScheme = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(raw) ? raw : 'http://' + raw;
    var u;
    try { u = new URL(withScheme); } catch (e) { return '地址格式不合法'; }
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return '只支持 http/https';
    if (u.username || u.password) return '不要带用户名或口令';
    if ((u.pathname && u.pathname !== '/') || u.search || u.hash) return '只接受 origin，不要带路径、查询串或 #';
    return '';
  }

  function testTarget() {
    var err = targetError();
    $('targetError').textContent = err;
    if (err) return;
    var box = $('testResult');
    box.innerHTML = '<div class="alert info"><span class="spin"></span> 正在测试…</div>';
    api('/api/test-target', { method: 'POST', body: { target_url: $('target_url').value.trim(), mode: state.mode } }).then(function (r) {
      if (r.reachable) {
        box.innerHTML = '<div class="alert ok">TCP 可达，耗时 ' + r.elapsed_ms + ' ms。保存时仍会再做一次目标检查。</div>';
      } else {
        box.innerHTML = '<div class="alert err">不可达：' + esc(r.error || 'unknown') + '（' + r.elapsed_ms + ' ms）。这不阻止保存，但生成后可能打不开。</div>';
      }
    }).catch(function (e) {
      box.innerHTML = '<div class="alert err">测试失败：' + esc(msgOf(e)) + '</div>';
    });
  }

  function validAppName(s) {
    return s.length >= 3 && s.length <= 32 && /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(s);
  }

  function checkName() {
    var name = $('appname').value.trim();
    var box = $('appnameMsg');
    if (!validAppName(name)) {
      box.className = 'error';
      box.textContent = '3–32 位，字母或数字开头，可用 . _ -。';
      return Promise.resolve(false);
    }
    box.className = 'help';
    box.textContent = '检查中…';
    return api('/api/appnames/check?appname=' + encodeURIComponent(name)).then(function (r) {
      if (r.ok) {
        box.className = 'help';
        box.textContent = '可用。';
        return true;
      }
      box.className = 'error';
      box.textContent = r.reason || '不可用';
      return false;
    }).catch(function (e) {
      box.className = 'help';
      box.textContent = '无法确认：' + msgOf(e);
      return true;
    });
  }

  function collectInput() {
    var input = {
      appname: $('appname').value.trim(),
      display_name: $('display_name').value.trim(),
      description: $('description').value.trim(),
      version: $('version').value.trim(),
      target_url: $('target_url').value.trim(),
      mode: state.mode,
      entry_title: $('entry_title').value.trim(),
      ui_type: $('ui_type').value,
      all_users: $('all_users').checked,
      no_display: $('no_display').checked,
      file_types: [],
      install_volume: $('install_volume').value,
      skip_tls_verify: $('skip_tls_verify').checked,
      allow_loopback: $('allow_loopback').checked
    };
    if (state.mode === 'reverse' && $('auth_on').checked) {
      input.basic_auth = { username: $('auth_user').value.trim(), password: $('auth_pass').value };
    }
    return input;
  }

  function renderSummary() {
    var d = collectInput();
    var dl = $('summary');
    var rows = [
      ['目标', d.target_url],
      ['模式', d.mode === 'direct' ? '直连 302' : '反向代理'],
      ['显示名称', d.display_name],
      ['appname', d.appname],
      ['入口', entryURL(d.appname)],
      ['安装卷', '卷 ' + d.install_volume],
      ['Basic Auth', d.basic_auth ? ('开启 · ' + d.basic_auth.username) : '关闭'],
      ['跳过证书校验', d.skip_tls_verify ? '是' : '否'],
      ['允许本机目标', d.allow_loopback ? '是' : '否'],
      ['桌面可见', d.all_users ? '所有用户' : '仅管理员']
    ];
    dl.innerHTML = rows.map(function (r) {
      return '<dt>' + esc(r[0]) + '</dt><dd>' + esc(r[1]) + '</dd>';
    }).join('');
  }

  function validateStep(n) {
    if (n === 1) {
      var e = targetError();
      $('targetError').textContent = e;
      return !e;
    }
    if (n === 2) {
      var ok = true;
      var dn = $('display_name').value.trim();
      if (!dn || dn.length > 32) {
        $('displayError').textContent = '显示名称需要 1–32 个字符';
        ok = false;
      } else $('displayError').textContent = '';
      var an = $('appname').value.trim();
      if (!validAppName(an)) {
        $('appnameMsg').className = 'error';
        $('appnameMsg').textContent = '3–32 位，字母或数字开头，可用 . _ -。';
        ok = false;
      }
      var desc = $('description').value.trim();
      if (desc.length > 120) {
        $('descError').textContent = '描述最多 120 个字符';
        ok = false;
      } else $('descError').textContent = '';
      if (!ok) return false;
      return checkName();
    }
    return true;
  }

  function next() {
    var n = state.step;
    if (n === 4) {
      submit();
      return;
    }
    if (!validateStep(n)) return;
    if (n === 1 && !$('description').value.trim()) {
      $('description').value = '通过代理访问 ' + $('target_url').value.trim();
    }
    if (n === 2) suggestAppNameIfNeeded();
    showStep(n + 1);
  }

  function suggestAppNameIfNeeded() {
    if (!$('appname').value) suggestAppName();
  }

  function submit() {
    var input = collectInput();
    if (!validAppName(input.appname)) { showStep(2); return; }
    $('nextBtn').disabled = true;
    var req = state.editId
      ? api('/api/targets/' + state.editId, { method: 'PUT', body: input })
      : api('/api/targets', { method: 'POST', body: input });
    req.then(function (t) {
      state.editId = t.spec.id;
      return api('/api/targets/' + t.spec.id + '/apply', { method: 'POST', body: { kind: '' } });
    }).then(function (r) {
      $('applyBox').style.display = '';
      showProgress(r.operation_id);
    }).catch(function (e) {
      $('nextBtn').disabled = false;
      $('applyBox').style.display = '';
      $('applyAlert').innerHTML = '<div class="alert err">' + esc(msgOf(e)) + '</div>';
    });
  }

  function stepIndex(step) {
    return { 'validate': 0, 'build': 1, 'install': 2, 'start': 3, 'done': 4 }[step] || 0;
  }

  function renderProgress(op, failed) {
    var steps = document.querySelectorAll('#progressSteps .pstep');
    var cur = stepIndex(op.step);
    for (var i = 0; i < steps.length; i++) {
      steps[i].className = 'pstep';
      if (failed && i === cur) steps[i].className += ' fail';
      else if (i < cur || (op.status === 'succeeded' && i <= cur)) steps[i].className += ' done';
      else if (i === cur && op.status === 'running') steps[i].className += ' active';
    }
  }

  function showProgress(opId) {
    if (state.polling) clearInterval(state.polling);
    $('applyBox').style.display = '';
    $('applyAlert').innerHTML = '<div class="alert info"><span class="spin"></span> 正在执行，请保持页面打开…</div>';
    $('applyLinks').innerHTML = '';
    var tick = function () {
      api('/api/operations/' + encodeURIComponent(opId)).then(function (op) {
        renderProgress(op, op.status === 'failed');
        if (op.status === 'running') return;
        clearInterval(state.polling);
        state.polling = null;
        $('nextBtn').disabled = false;
        if (op.status === 'succeeded') {
          $('applyAlert').innerHTML = '<div class="alert ok">安装完成，应用已进入运行状态。</div>';
          var links = '<a class="btn primary sm" target="_blank" rel="noopener" href="' + esc(entryURL($('appname').value)) + '">打开入口</a>';
          if (state.editId) {
            links += ' <a class="btn sm" href="/api/targets/' + encodeURIComponent(state.editId) + '/package">下载 fpk</a>';
            links += ' <button class="btn sm" type="button" id="backList">返回列表</button>';
          }
          $('applyLinks').innerHTML = links;
          var bl = $('backList');
          if (bl) bl.addEventListener('click', function () { switchTab('list'); loadListCached(); });
          loadListCached();
        } else {
          $('applyAlert').innerHTML = '<div class="alert err">操作失败：' + esc(op.error || '未知错误') + '</div>' +
            '<div style="margin-top:10px"><a class="btn sm" href="/api/targets/' + encodeURIComponent(state.editId) + '/package">下载 fpk</a> <button class="btn sm" type="button" id="retryApply">重试安装</button></div>';
          var r = $('retryApply');
          if (r) r.addEventListener('click', function () { applyTarget(state.editId, 'reinstall'); });
          loadListCached();
        }
      }).catch(function (e) {
        clearInterval(state.polling);
        state.polling = null;
        $('applyAlert').innerHTML = '<div class="alert err">查询进度失败：' + esc(msgOf(e)) + '</div>';
        $('nextBtn').disabled = false;
      });
    };
    tick();
    state.polling = setInterval(tick, 1200);
  }

  function init() {
    loadHealth();
    loadListCached();
    loadVolumes();

    $('tabList').addEventListener('click', function () { switchTab('list'); loadListCached(); });
    $('tabWizard').addEventListener('click', function () {
      switchTab('wizard');
      if (!state.editId) resetForm();
    });
    $('newBtn').addEventListener('click', startNew);
    $('refreshBtn').addEventListener('click', function () { loadListCached(); loadHealth(); });
    $('listBody').addEventListener('click', onListClick);
    $('testBtn').addEventListener('click', testTarget);
    $('checkNameBtn').addEventListener('click', checkName);
    $('nextBtn').addEventListener('click', next);
    $('backBtn').addEventListener('click', function () { if (state.step > 1) showStep(state.step - 1); });
    $('cancelBtn').addEventListener('click', function () {
      switchTab('list');
      if (state.polling) { clearInterval(state.polling); state.polling = null; }
    });

    $('display_name').addEventListener('input', function () {
      if (!state.appnameTouched) suggestAppName();
      $('displayError').textContent = '';
    });
    $('appname').addEventListener('input', function () {
      state.appnameTouched = true;
      $('appnameMsg').className = 'help';
      $('appnameMsg').textContent = '3–32 位，字母或数字开头，可用 . _ -。';
    });
    $('target_url').addEventListener('input', function () { $('targetError').textContent = ''; });
    document.querySelectorAll('.mode-card').forEach(function (b) {
      b.addEventListener('click', function () { setMode(b.getAttribute('data-mode')); });
    });
    $('auth_on').addEventListener('change', function () {
      $('authFields').style.display = $('auth_on').checked ? '' : 'none';
    });
  }

  document.addEventListener('DOMContentLoaded', init);
})();
