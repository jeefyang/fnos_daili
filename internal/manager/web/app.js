(function () {
  'use strict';

  var token = (document.querySelector('meta[name="csrf-token"]') || {}).content || '';
  var state = {
    tab: 'list',
    step: 1,
    editId: null,
    originalAppname: '',
    appnameTouched: false,
    mode: 'reverse',
    volumesLoaded: false,
    polling: null,
    lastPackage: null,
    lastOpAppName: '',
    lastOpPort: 0,
    editPort: 0,
    editVolume: '',
    editHasIcon: false,
    iconData: '',
    iconRemove: false,
    settings: { appname_prefix: 'proxy.', appname_suffix: '' },
    network: { known: false, lan: null, client_ip: '' }
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

  function waitOperation(opId) {
    return new Promise(function (resolve, reject) {
      var started = Date.now();
      var tick = function () {
        api('/api/operations/' + encodeURIComponent(opId)).then(function (op) {
          if (op.status === 'running') {
            if (Date.now() - started > 10 * 60 * 1000) {
              reject(new Error('操作超时，请到列表查看状态'));
              return;
            }
            setTimeout(tick, 1000);
            return;
          }
          resolve(op);
        }).catch(reject);
      };
      tick();
    });
  }

  function loadSettings() {
    return api('/api/settings').then(function (s) {
      if (s && typeof s.appname_prefix === 'string') {
        state.settings.appname_prefix = s.appname_prefix;
        state.settings.appname_suffix = s.appname_suffix || '';
      }
    }).catch(function () {});
  }

  function loadNetwork() {
    return api('/api/network').then(function (n) {
      state.network = n || { known: false, lan: null };
      if (window.__lastList) renderList(window.__lastList);
      renderNetBanner();
    }).catch(function () {});
  }

  function renderNetBanner() {
    var b = $('netBanner');
    if (!b) return;
    if (fnConnectHost()) {
      b.style.display = '';
      b.textContent = '当前通过飞牛 FN Connect 访问：入口使用应用子域（https://<appname>-main.<当前域名>/）。若打不开，请先在 FN Connect 里发布该应用。';
    } else if (state.network && state.network.lan === false) {
      b.style.display = '';
      b.textContent = '当前看起来是通过外网访问。列表里的入口使用飞牛所在局域网的独立端口，通常没有映射到公网，直接打开可能打不开。';
    } else {
      b.style.display = 'none';
      b.textContent = '';
    }
  }

  function copyText(text) {
    var done = function () { toast('已复制入口地址'); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { fallbackCopy(text, done); });
    } else {
      fallbackCopy(text, done);
    }
  }

  function fallbackCopy(text, done) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); done(); } catch (e) { window.prompt('复制入口地址', text); }
    ta.remove();
  }

  function openEntry(url) {
    if (!url) return;
    if (!fnConnectHost() && state.network && state.network.lan === false) {
      openModal('外网访问提示',
        '<div class="alert warn">当前看起来是通过外网访问。这个入口是飞牛所在局域网的独立端口，通常没有映射到公网，直接打开多半不可达。</div>' +
        '<div class="field" style="margin-top:12px"><label>入口地址</label><div class="mono" style="word-break:break-all">' + esc(url) + '</div></div>',
        [
          { label: '复制地址', onClick: function () { copyText(url); } },
          { label: '仍然尝试', cls: 'primary', onClick: function (close) { close(); window.open(url, '_blank', 'noopener'); } }
        ]);
      return;
    }
    window.open(url, '_blank', 'noopener');
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

  // FN Connect serves every app entry under its own subdomain:
  // https://<fnDomain>.<current host>/<path>. fnDomain is the entry key with
  // dots replaced by hyphens; the default "Application" suffix is dropped.
  // Our generated entries are always "<appname>.main", so the subdomain is
  // "<appname dots->hyphens>-main". See docs/research/fnos-fnconnect-entry-url.md.
  var FN_CONNECT_SUFFIXES = ['.fnos.net', '.5ddd.com', '.trzznas.com'];

  function fnConnectHost() {
    var host = String(location.hostname || '').toLowerCase();
    for (var i = 0; i < FN_CONNECT_SUFFIXES.length; i++) {
      var s = FN_CONNECT_SUFFIXES[i];
      if (host.length > s.length && host.slice(-s.length) === s) return host;
    }
    return '';
  }

  function fnEntryHost(appname) {
    if (!appname) return '';
    return String(appname).toLowerCase().replace(/\./g, '-') + '-main';
  }

  function entryURL(appname, port) {
    var fnHost = fnConnectHost();
    if (fnHost) return appname ? 'https://' + fnEntryHost(appname) + '.' + fnHost + '/' : '';
    if (!port) return '';
    var host = location.hostname || 'localhost';
    if (host.indexOf(':') >= 0 && host.charAt(0) !== '[') host = '[' + host + ']';
    return 'http://' + host + ':' + port + '/';
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
      window.__lastList = data;
      renderList(data);
    }).catch(function (e) {
      $('listBody').innerHTML = '<div class="empty"><div class="big">加载失败</div><div>' + esc(msgOf(e)) + '</div></div>';
    });
  }

  function renderList(data) {
    window.__lastList = data;
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
        var url = entryURL(sp.appname, sp.port);
        var fn = !!fnConnectHost();
        var lanBlocked = !fn && state.network && state.network.lan === false;
        var label = fn ? url.replace(/^https:\/\//, '') : ':' + (sp.port || '') + '/';
        var entryHtml = url
          ? '<a class="mono entry-link" style="font-size:11.5px;word-break:break-all" href="' + esc(url) + '" data-entry="1" data-url="' + esc(url) + '" target="_blank" rel="noopener">' + esc(label) + '</a>'
          : '<span class="faint">未分配</span>';
        if (url && fn) entryHtml += '<div class="faint" style="font-size:11px;margin-top:3px">FN Connect 应用子域</div>';
        if (url && lanBlocked) entryHtml += '<div class="faint" style="font-size:11px;margin-top:3px">外网访问，端口通常不可达</div>';
        var actions = [];
        if (rt.state === 'running' || rt.state === 'installed_not_running') {
          actions.push('<button class="btn sm" data-act="open" data-id="' + esc(sp.id) + '" data-url="' + esc(url) + '" type="button">打开</button>');
        }
        actions.push('<button class="btn sm" data-act="edit" data-id="' + esc(sp.id) + '" type="button">编辑</button>');
        actions.push('<button class="btn sm" data-act="apply" data-id="' + esc(sp.id) + '" type="button">' + (rt.state === 'draft' ? '安装' : '重新应用') + '</button>');
        if (rt.last_known_good_path) actions.push('<button class="btn sm" data-act="rollback" data-id="' + esc(sp.id) + '" type="button">回滚</button>');
        if (rt.package_path) actions.push('<a class="btn sm" href="' + apiURL('/api/targets/' + encodeURIComponent(sp.id) + '/package') + '">下载</a>');
        actions.push('<button class="btn sm" data-act="log" data-id="' + esc(sp.id) + '" type="button">日志</button>');
        actions.push('<button class="btn sm danger" data-act="delete" data-id="' + esc(sp.id) + '" type="button">删除</button>');
        return '<tr>' +
          '<td class="app-cell"><div class="name">' + esc(sp.display_name) + '</div><div class="id mono">' + esc(sp.appname) + '</div></td>' +
          '<td class="target-cell"><div class="mono">' + esc(sp.target_url) + '</div><div class="faint">' + (sp.mode === 'direct' ? '直连 302' : '反向代理') + '</div></td>' +
          '<td><span class="pill ' + st.cls + '"><span class="dot"></span>' + st.text + '</span>' + (rt.needs_reapply ? '<div class="faint" style="font-size:11px;margin-top:3px">待重新应用</div>' : '') + '</td>' +
          '<td class="nowrap">' + entryHtml + '</td>' +
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
    var link = e.target.closest('a[data-entry]');
    if (link) {
      e.preventDefault();
      openEntry(link.getAttribute('data-url'));
      return;
    }
    var btn = e.target.closest('button[data-act]');
    if (!btn) return;
    var id = btn.getAttribute('data-id');
    var act = btn.getAttribute('data-act');
    if (act === 'open') {
      var t = findTarget(id);
      openEntry(btn.getAttribute('data-url') || (t ? entryURL(t.spec.appname, t.spec.port) : ''));
    } else if (act === 'edit') {
      var t2 = findTarget(id);
      if (t2) startEdit(t2);
    } else if (act === 'apply') {
      if (!confirm('将重新生成安装包并安装。已安装的同名应用会先卸载，继续？')) return;
      applyTarget(id, '');
    } else if (act === 'rollback') {
      if (!confirm('用上一版安装包重装，继续？')) return;
      postOp('/api/targets/' + id + '/rollback').then(function (r) {
        return waitOperation(r.operation_id);
      }).then(function (op) {
        if (op && op.status === 'failed') toast('回滚失败：' + (op.error || '未知错误'), true);
        else toast('回滚完成');
        return loadListCached();
      }).catch(function (err) { toast(msgOf(err), true); });
    } else if (act === 'delete') {
      if (!confirm('卸载应用并删除托管记录？安装包也会删除。')) return;
      postOp('/api/targets/' + id + '/delete').then(function (r) {
        return waitOperation(r.operation_id);
      }).then(function (op) {
        if (op && op.status === 'failed') toast('删除失败：' + (op.error || '未知错误'), true);
        else toast('已删除');
        return loadListCached();
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
    var t = findTarget(id);
    if (t && state.tab !== 'wizard') fillForm(t);
    state.editId = id;
    if (t) { state.lastOpAppName = t.spec.appname; state.lastOpPort = t.spec.port; }
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
      if (el) el.addEventListener('click', function () { (b.onClick || function (close) { close(); })(closeModal); });
    });
  }
  function closeModal() { $('modalRoot').innerHTML = ''; }

  function switchTab(name) {
    state.tab = name;
    $('listView').style.display = name === 'list' ? '' : 'none';
    $('wizardView').style.display = name === 'wizard' ? '' : 'none';
    if (name === 'list') renderNetBanner();
  }

  function setIconPreview(src) {
    var img = $('iconPreview');
    if (img) img.src = src;
  }

  function defaultIconURL() { return apiURL('/api/default-icon'); }

  function onIconPick() {
    var fileInput = $('iconFile');
    var errBox = $('iconError');
    if (!fileInput || !fileInput.files || !fileInput.files.length) return;
    var file = fileInput.files[0];
    errBox.textContent = '';
    if (file.type !== 'image/png' && file.type !== 'image/jpeg') {
      errBox.textContent = '只支持 PNG 或 JPEG 图片';
      fileInput.value = '';
      return;
    }
    if (file.size > 2 * 1024 * 1024) {
      errBox.textContent = '图片不能超过 2MB';
      fileInput.value = '';
      return;
    }
    var reader = new FileReader();
    reader.onload = function () {
      state.iconData = String(reader.result || '');
      state.iconRemove = false;
      setIconPreview(state.iconData);
      errBox.textContent = '';
    };
    reader.onerror = function () { errBox.textContent = '读取图片失败，请重试'; };
    reader.readAsDataURL(file);
  }

  function onIconReset() {
    state.iconData = '';
    state.iconRemove = !!state.editId && state.editHasIcon;
    $('iconFile').value = '';
    $('iconError').textContent = '';
    setIconPreview(defaultIconURL());
  }

  function resetForm() {
    state.editId = null;
    state.originalAppname = '';
    state.appnameTouched = false;
    state.mode = 'reverse';
    state.lastPackage = null;
    state.editPort = 0;
    state.editVolume = '';
    state.editHasIcon = false;
    state.iconData = '';
    state.iconRemove = false;
    $('iconFile').value = '';
    $('iconError').textContent = '';
    setIconPreview(defaultIconURL());
    $('target_url').value = '';
    $('display_name').value = '';
    $('appname').value = '';
    $('appname').disabled = false;
    if (state.volumesLoaded && $('install_volume').options.length) $('install_volume').selectedIndex = 0;
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
    $('wizardTitle').textContent = '新建代理';
    $('wizardSub').textContent = '填一个 ip:port，生成并安装一个独立的代理应用。';
    resetForm();
  }

  function startEdit(t) {
    switchTab('wizard');
    $('wizardTitle').textContent = '编辑代理';
    $('wizardSub').textContent = '修改会重新生成安装包并重装；应用标识首次安装后不可更改。';
    fillForm(t);
  }

  function fillForm(t) {
    resetForm();
    if (!t) return;
    state.editId = t.spec.id;
    state.originalAppname = t.spec.appname;
    state.editPort = t.spec.port || 0;
    state.editVolume = t.spec.install_volume || '1';
    state.editHasIcon = !!t.spec.has_icon;
    if (state.editHasIcon) {
      setIconPreview(apiURL('/api/targets/' + encodeURIComponent(t.spec.id) + '/icon?v=' + Date.now()));
    }
    state.appnameTouched = true;
    state.lastOpAppName = t.spec.appname;
    state.lastOpPort = t.spec.port || 0;
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
      var list = d.volumes || [d.default || '1'];
      var want = state.editVolume || sel.value;
      sel.innerHTML = '';
      list.forEach(function (v) {
        var o = document.createElement('option');
        o.value = v;
        o.textContent = '卷 ' + v + (v === d.default ? '（默认）' : '');
        sel.appendChild(o);
      });
      sel.value = (want && list.indexOf(String(want)) >= 0) ? String(want) : (d.default || '1');
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
    var base = slugify($('display_name').value) || 'app';
    var prefix = typeof state.settings.appname_prefix === 'string' ? state.settings.appname_prefix : 'proxy.';
    var suffix = typeof state.settings.appname_suffix === 'string' ? state.settings.appname_suffix : '';
    var room = 27 - prefix.length - suffix.length;
    if (room < 1) {
      prefix = 'proxy.';
      suffix = '';
      room = 21;
    }
    base = base.slice(0, room);
    var name = prefix + base + suffix;
    if (!validAppName(name)) name = prefix + 'app' + suffix;
    $('appname').value = name.slice(0, 27) + '.' + randHex4();
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
    if (state.editId && name === state.originalAppname) {
      box.className = 'help';
      box.textContent = '当前应用标识，首次安装后不可更改。';
      return Promise.resolve(true);
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
    if (state.iconData) input.icon_data = state.iconData;
    if (state.iconRemove) input.remove_icon = true;
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
      ['入口', fnConnectHost() ? (d.appname ? entryURL(d.appname, state.editPort) : '生成时按 appname 生成应用子域') : (state.editPort ? entryURL(d.appname, state.editPort) : '生成时自动分配端口')],
      ['安装卷', '卷 ' + d.install_volume],
      ['图标', state.iconData ? '自定义（待保存）' : (state.iconRemove ? '恢复默认' : (state.editHasIcon ? '当前为自定义' : '默认'))],
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
      state.editPort = t.spec.port || 0;
      state.lastOpAppName = t.spec.appname;
      state.lastOpPort = t.spec.port || 0;
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
          toast('安装完成，应用已进入运行状态');
          switchTab('list');
          loadListCached();
        } else {
          $('applyAlert').innerHTML = '<div class="alert err">操作失败：' + esc(op.error || '未知错误') + '</div>' +
            '<div style="margin-top:10px"><a class="btn sm" href="' + apiURL('/api/targets/' + encodeURIComponent(state.editId) + '/package') + '">下载 fpk</a> <button class="btn sm" type="button" id="retryApply">重试安装</button></div>';
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

  function openSettings() {
    api('/api/settings').then(function (s) {
      state.settings.appname_prefix = s.appname_prefix || '';
      state.settings.appname_suffix = s.appname_suffix || '';
      var prefix = esc(state.settings.appname_prefix);
      var suffix = esc(state.settings.appname_suffix);
      openModal('全局设置',
        '<p class="lede" style="margin-top:0">应用标识的公共前缀与后缀，只影响新建时的自动建议，不会改动已创建的应用。</p>' +
        '<div class="grid-2">' +
        '<div class="field"><label for="setPrefix">前缀</label><input id="setPrefix" class="input mono" maxlength="16" value="' + prefix + '" placeholder="proxy."><div class="help">最多 16 字节，只允许字母、数字、. _ -；非空时必须以字母或数字开头。</div></div>' +
        '<div class="field"><label for="setSuffix">后缀</label><input id="setSuffix" class="input mono" maxlength="16" value="' + suffix + '" placeholder=".prod"><div class="help">最多 16 字节；前后缀合计不超过 24 字节。</div></div>' +
        '</div>' +
        '<div class="field"><label>预览</label><div id="affixPreview" class="mono faint"></div><div id="settingsError" class="error"></div></div>',
        [
          { label: '取消' },
          { label: '保存', cls: 'primary', onClick: function (close) { saveSettings(close); } }
        ]);
      ['setPrefix', 'setSuffix'].forEach(function (id) {
        var el = $(id);
        if (el) el.addEventListener('input', updateAffixPreview);
      });
      updateAffixPreview();
    }).catch(function (err) { toast(msgOf(err), true); });
  }

  function settingsInput() {
    return {
      appname_prefix: $('setPrefix') ? $('setPrefix').value : '',
      appname_suffix: $('setSuffix') ? $('setSuffix').value : ''
    };
  }

  function updateAffixPreview() {
    if (!$('affixPreview')) return;
    var s = settingsInput();
    var sample = (s.appname_prefix || '') + 'home' + (s.appname_suffix || '') + '.9f3a';
    $('affixPreview').textContent = sample;
    if ($('settingsError')) $('settingsError').textContent = '';
  }

  function saveSettings(close) {
    var s = settingsInput();
    api('/api/settings', { method: 'PUT', body: s }).then(function (out) {
      state.settings.appname_prefix = out.appname_prefix || '';
      state.settings.appname_suffix = out.appname_suffix || '';
      if (!state.editId && !state.appnameTouched && $('display_name').value.trim()) suggestAppName();
      toast('设置已保存');
      close();
    }).catch(function (err) {
      if ($('settingsError')) $('settingsError').textContent = msgOf(err);
      else toast(msgOf(err), true);
    });
  }

  function init() {
    loadHealth();
    loadSettings();
    loadNetwork();
    loadListCached();
    loadVolumes();

    $('settingsBtn').addEventListener('click', openSettings);
    $('newBtn').addEventListener('click', startNew);
    $('wizardBackBtn').addEventListener('click', function () {
      switchTab('list');
      loadListCached();
    });
    $('refreshBtn').addEventListener('click', function () {
      loadListCached();
      loadHealth();
      loadNetwork();
    });
    $('listBody').addEventListener('click', onListClick);
    $('testBtn').addEventListener('click', testTarget);
    $('checkNameBtn').addEventListener('click', checkName);
    $('iconPick').addEventListener('click', function () { $('iconFile').click(); });
    $('iconFile').addEventListener('change', onIconPick);
    $('iconReset').addEventListener('click', onIconReset);
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
