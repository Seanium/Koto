export function graphemes(text, locale) {
  return typeof Intl.Segmenter === 'function'
    ? [...new Intl.Segmenter(locale, { granularity: 'grapheme' }).segment(text)].map(part => part.segment)
    : Array.from(text);
}

// Hidden characters keep the full phrase in flow; only opacity and the cursor change.
export function createTypewriter(element, motion, page = element.ownerDocument, timers = globalThis) {
  let timer;
  let characters = [];
  let cursor;
  let visible = 0;
  function cancel() { timers.clearTimeout(timer); timer = undefined; }
  function positionCursor() {
    if (!cursor || !characters.length) return;
    const character = characters[Math.max(0, visible - 1)].getBoundingClientRect();
    const origin = element.getBoundingClientRect();
    cursor.style.left = `${(visible ? character.right : character.left) - origin.left}px`;
    cursor.style.top = `${character.top + character.height / 2 - origin.top}px`;
  }
  function setPhase(phase) { element.dataset.phase = phase; positionCursor(); }
  function showFull() {
    cancel();
    for (const character of characters) character.style.opacity = '1';
    visible = characters.length;
    setPhase('paused');
  }
  function schedule(callback, delay) {
    cancel();
    timer = timers.setTimeout(() => {
      timer = undefined;
      if (page.hidden || motion.matches) { showFull(); return; }
      callback();
    }, delay);
  }
  function rest() { setPhase('resting'); schedule(erase, 3500); }
  function type() {
    setPhase('typing');
    characters[visible++].style.opacity = '1';
    positionCursor();
    if (visible < characters.length) schedule(type, Math.min(110, 1700 / characters.length));
    else rest();
  }
  function erase() {
    setPhase('deleting');
    characters[--visible].style.opacity = '0';
    positionCursor();
    if (visible) schedule(erase, Math.min(70, 1200 / characters.length));
    else { setPhase('waiting'); schedule(type, 320); }
  }
  function resume() {
    showFull();
    if (!page.hidden && !motion.matches && characters.length) rest();
  }
  motion.addEventListener('change', resume);
  page.addEventListener('visibilitychange', resume);
  page.defaultView?.addEventListener('resize', positionCursor);
  return {
    play(text, locale) {
      cancel();
      characters = graphemes(text, locale).map(value => {
        const span = element.ownerDocument.createElement('span');
        span.className = 'typed-character';
        span.textContent = value;
        span.style.opacity = '0';
        return span;
      });
      cursor = element.ownerDocument.createElement('span');
      cursor.className = 'type-cursor';
      cursor.setAttribute('aria-hidden', 'true');
      element.replaceChildren(...characters, cursor);
      visible = 0;
      setPhase('waiting');
      if (motion.matches || page.hidden || !characters.length) showFull();
      else schedule(type, 140);
    }
  };
}

export function cycleAgentLogos(logos, motion, page = document, timers = globalThis) {
  let active = 0;
  let timer;
  function schedule() {
    timers.clearInterval(timer);
    if (page.hidden || motion.matches || logos.length < 2) return;
    timer = timers.setInterval(() => {
      logos[active].classList.remove('is-active');
      active = (active + 1) % logos.length;
      logos[active].classList.add('is-active');
    }, 3000);
  }
  motion.addEventListener('change', schedule);
  page.addEventListener('visibilitychange', schedule);
  schedule();
}

// Each complete line stays in flow while its characters become visible, avoiding layout shifts.
export function createDialogue(elements, motion, page = document, timers = globalThis) {
  let states = [];
  let line = 0;
  let visible = 0;
  let timer;
  function cancel() { timers.clearTimeout(timer); timer = undefined; }
  function positionCursor() {
    const state = states[line];
    if (!state || !state.characters.length) return;
    const character = state.characters[Math.max(0, visible - 1)].getBoundingClientRect();
    const origin = state.element.getBoundingClientRect();
    state.cursor.style.left = `${(visible ? character.right : character.left) - origin.left}px`;
    state.cursor.style.top = `${character.top + character.height / 2 - origin.top}px`;
  }
  function showFull() {
    cancel();
    for (const state of states) {
      for (const character of state.characters) character.style.opacity = '1';
      state.cursor.hidden = true;
    }
  }
  function schedule(callback, delay) {
    cancel();
    if (page.hidden || motion.matches) return;
    timer = timers.setTimeout(() => {
      timer = undefined;
      if (!page.hidden && !motion.matches) callback();
    }, delay);
  }
  function type() {
    const state = states[line];
    if (!state) { schedule(start, 5500); return; }
    state.cursor.hidden = false;
    if (visible < state.characters.length) {
      state.characters[visible++].style.opacity = '1';
      positionCursor();
      schedule(type, line === elements.length - 1 ? 24 : 42);
    } else {
      state.cursor.hidden = true;
      line++;
      visible = 0;
      schedule(type, line === 1 ? 600 : 220);
    }
  }
  function start() {
    cancel();
    for (const state of states) {
      for (const character of state.characters) character.style.opacity = '0';
      state.cursor.hidden = true;
    }
    line = 0;
    visible = 0;
    schedule(type, 240);
  }
  function resume() {
    showFull();
    if (!page.hidden && !motion.matches) schedule(start, 1800);
  }
  motion.addEventListener('change', resume);
  page.addEventListener('visibilitychange', resume);
  page.defaultView?.addEventListener('resize', positionCursor);
  return {
    play(lines, locale) {
      cancel();
      states = elements.map((element, index) => {
        const characters = graphemes(lines[index], locale).map(value => {
          const span = page.createElement('span');
          span.textContent = value;
          return span;
        });
        const cursor = page.createElement('span');
        cursor.className = 'dialogue-cursor';
        cursor.hidden = true;
        element.replaceChildren(...characters, cursor);
        return { element, characters, cursor };
      });
      if (motion.matches || page.hidden) showFull();
      else start();
    }
  };
}
