// 鹈鹕腿部动画测量（注入到鹈鹕 HTML 的副本里，在无头浏览器中运行）。
// 每次采样把 SVG（SMIL）和 CSS 动画拨到指定时刻；JS 逐帧动画靠定时器自然推进。
// 记录：每条腿各个关节点的屏幕坐标、踏板中心、车轮的旋转角。分类在 probe.py 里做。
// 结果写进页面末尾一个 id 为 __legprobe__ 的 pre 元素，由 probe.py 用 --dump-dom 取回。
(function () {
  // 无头模式下 requestAnimationFrame 不一定按时触发，换成定时器，JS 逐帧动画也能跑起来
  window.requestAnimationFrame = (cb) => setTimeout(() => cb(performance.now()), 16);

  const LEG = /leg|thigh|shin|calf|knee|tibia|腿|膝/i;
  const PEDAL = /pedal(?!ing)|crank|踏板|曲柄|脚踏/i; // pedaling（“正在蹬”）常用来命名整组腿，不算踏板
  const WHEEL = /wheel|spoke|tire|tyre|rim|车轮|轮子|辐条/i;
  const SAMPLES = 24, STEP = 0.1; // 2.4 秒，常见动画周期 0.8–1.5 秒，至少覆盖一圈

  // 部件归属看离元素最近的命名：从元素往上找，第一个命中的 id / class / 紧挨着的前置注释决定类别。
  // 例如腿在一个注释写着“由曲柄驱动”的组里，腿自己的 id 是 near-leg，就算腿。
  function kind(el) {
    for (let n = el; n && n.nodeType === 1 && n.tagName.toLowerCase() !== 'svg'; n = n.parentNode) {
      const name = (n.getAttribute('id') || '') + ' ' + (n.getAttribute('class') || '');
      for (const [k, re] of [['leg', LEG], ['pedal', PEDAL], ['wheel', WHEEL]]) if (re.test(name)) return k;
      for (let p = n.previousSibling, i = 0; p && i < 6; p = p.previousSibling, i++) {
        if (p.nodeType === 8) {
          for (const [k, re] of [['leg', LEG], ['pedal', PEDAL], ['wheel', WHEEL]]) if (re.test(p.textContent)) return k;
          break;
        }
        if (p.nodeType === 1) break;
      }
    }
    return null;
  }

  // 取路径各段的端点（直线段精确；曲线只取端点，足够判断关节）
  function vertices(el) {
    const tag = el.tagName.toLowerCase();
    let pts = [];
    if (tag === 'line') {
      pts = [[+el.getAttribute('x1') || 0, +el.getAttribute('y1') || 0], [+el.getAttribute('x2') || 0, +el.getAttribute('y2') || 0]];
      // SMIL 改 x1/y2 时读动画值
      const a = (n) => el[n] && el[n].animVal ? el[n].animVal.value : null;
      if (a('x1') !== null) pts = [[a('x1'), a('y1')], [a('x2'), a('y2')]];
    } else if (tag === 'polyline' || tag === 'polygon') {
      pts = [...el.animatedPoints].map((p) => [p.x, p.y]);
    } else {
      // 浏览器里 d 是 CSS 属性，计算样式里是当前生效的路径（含 SMIL 动画和 JS 改的值）
      let d = getComputedStyle(el).d;
      d = d && d.startsWith('path(') ? d.slice(6, -2) : el.getAttribute('d') || '';
      const re = /([MLHVCSQTAZmlhvcsqtaz])([^MLHVCSQTAZmlhvcsqtaz]*)/g;
      let x = 0, y = 0, sx = 0, sy = 0, m;
      while ((m = re.exec(d))) {
        const c = m[1], n = (m[2].match(/-?\d*\.?\d+(?:e-?\d+)?/gi) || []).map(Number);
        const rel = c === c.toLowerCase();
        const per = { m: 2, l: 2, h: 1, v: 1, c: 6, s: 4, q: 4, t: 2, a: 7, z: 0 }[c.toLowerCase()];
        if (per === 0) { x = sx; y = sy; pts.push([x, y]); continue; }
        for (let i = 0; i + per <= n.length; i += per) {
          const g = n.slice(i, i + per);
          switch (c.toLowerCase()) {
            case 'h': x = rel ? x + g[0] : g[0]; break;
            case 'v': y = rel ? y + g[0] : g[0]; break;
            default: x = rel ? x + g[per - 2] : g[per - 2]; y = rel ? y + g[per - 1] : g[per - 1];
          }
          if (c.toLowerCase() === 'm' && i === 0) { sx = x; sy = y; }
          pts.push([x, y]);
        }
      }
    }
    const ctm = el.getScreenCTM();
    return pts.map(([px, py]) => { const p = new DOMPoint(px, py).matrixTransform(ctm); return [+p.x.toFixed(2), +p.y.toFixed(2)]; });
  }

  function center(el) {
    const b = el.getBBox(), m = el.getScreenCTM();
    const p = new DOMPoint(b.x + b.width / 2, b.y + b.height / 2).matrixTransform(m);
    return [+p.x.toFixed(2), +p.y.toFixed(2)];
  }

  function setTime(t) {
    document.querySelectorAll('svg').forEach((s) => { try { s.pauseAnimations(); s.setCurrentTime(t); } catch (e) {} });
    document.getAnimations().forEach((a) => { try { a.pause(); a.currentTime = t * 1000; } catch (e) {} });
  }

  const all = (sel) => [...document.querySelectorAll(sel)];
  function sample(t) {
    setTime(t);
    document.body.offsetHeight; // 强制刷新样式，让计算样式和变换矩阵用上新时刻
    // 隐藏的元素（比如“减少动态效果”时才显示的静止腿）不算
    const visible = (el) => (el.checkVisibility ? el.checkVisibility() : true) && el.getBBox().width + el.getBBox().height > 0;
    const legs = all('path,line,polyline').filter((el) => kind(el) === 'leg' && visible(el));
    const pedals = all('svg *').filter((el) => el.getBBox && kind(el) === 'pedal');
    const wheels = all('svg g, svg use, svg circle, svg path, svg line').filter((el) => kind(el) === 'wheel');
    return {
      t,
      legs: legs.map((el) => { try { return vertices(el); } catch (e) { return null; } }),
      pedals: pedals.map((el) => { try { return center(el); } catch (e) { return null; } }).filter(Boolean),
      // 车轮旋转角：元素坐标系相对屏幕的旋转（转动的辐条组会随时间变）
      wheels: wheels.map((el) => { try { const m = el.getScreenCTM(); return +(Math.atan2(m.b, m.a) * 180 / Math.PI).toFixed(2); } catch (e) { return null; } }),
    };
  }

  const frames = [];
  let k = 0;
  function tick() {
    frames.push(sample(k * STEP));
    if (++k < SAMPLES) setTimeout(tick, STEP * 1000);
    else {
      const pre = document.createElement('pre');
      pre.id = '__legprobe__';
      pre.textContent = JSON.stringify(frames);
      document.body.appendChild(pre);
    }
  }
  window.addEventListener('load', () => setTimeout(tick, 300));
})();
