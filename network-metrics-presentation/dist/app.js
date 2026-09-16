(() => {
  const slides = [...document.querySelectorAll('.slide')];
  const counter = document.querySelector('#counter');
  const progress = document.querySelector('#progress');
  let index = Math.max(0, Math.min(slides.length - 1, Number(location.hash.slice(1)) - 1 || 0));

  function render(nextIndex, updateHash = true) {
    index = Math.max(0, Math.min(slides.length - 1, nextIndex));
    slides.forEach((slide, slideIndex) => {
      const active = slideIndex === index;
      slide.classList.toggle('active', active);
      slide.setAttribute('aria-hidden', String(!active));
    });
    counter.textContent = `${String(index + 1).padStart(2, '0')} / ${String(slides.length).padStart(2, '0')}`;
    progress.style.width = `${((index + 1) / slides.length) * 100}%`;
    document.title = `${slides[index].dataset.title} · Dagger network accounting`;
    if (updateHash) history.replaceState(null, '', `#${index + 1}`);
  }

  const next = () => render(index + 1);
  const previous = () => render(index - 1);
  document.querySelector('#next').addEventListener('click', next);
  document.querySelector('#prev').addEventListener('click', previous);
  document.querySelector('#fullscreen').addEventListener('click', () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else document.documentElement.requestFullscreen();
  });

  document.addEventListener('keydown', (event) => {
    if (['ArrowRight', 'PageDown', ' '].includes(event.key)) {
      event.preventDefault();
      next();
    } else if (['ArrowLeft', 'PageUp'].includes(event.key)) {
      event.preventDefault();
      previous();
    } else if (event.key === 'Home') {
      render(0);
    } else if (event.key === 'End') {
      render(slides.length - 1);
    } else if (event.key.toLowerCase() === 'f') {
      document.querySelector('#fullscreen').click();
    }
  });

  window.addEventListener('hashchange', () => {
    const requested = Number(location.hash.slice(1)) - 1;
    if (Number.isInteger(requested)) render(requested, false);
  });

  let touchStartX = null;
  document.addEventListener('touchstart', (event) => {
    touchStartX = event.changedTouches[0].clientX;
  }, { passive: true });
  document.addEventListener('touchend', (event) => {
    if (touchStartX === null) return;
    const delta = event.changedTouches[0].clientX - touchStartX;
    if (Math.abs(delta) > 60) delta < 0 ? next() : previous();
    touchStartX = null;
  }, { passive: true });

  render(index, false);
})();
