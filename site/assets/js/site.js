// pheme.rh1.tech — reveal on scroll and the mobile menu. No dependencies, no build step.
(function () {
  'use strict';

  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // Mobile burger menu.
  var header = document.querySelector('.site-header');
  var toggle = document.querySelector('.nav-toggle');
  if (header && toggle) {
    var setOpen = function (open) {
      header.classList.toggle('nav-open', open);
      toggle.setAttribute('aria-expanded', String(open));
    };
    toggle.addEventListener('click', function () {
      setOpen(!header.classList.contains('nav-open'));
    });
    // Close when a link is chosen, when tapping outside, or on Escape — all three,
    // because a menu that only closes one way is a menu somebody gets stuck in.
    header.addEventListener('click', function (e) {
      if (e.target.closest('.site-nav a')) setOpen(false);
    });
    document.addEventListener('click', function (e) {
      if (!header.contains(e.target)) setOpen(false);
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') setOpen(false);
    });
  }

  // Reveal-on-scroll. Unobserved after firing: this is a one-way transition and
  // re-animating on the way back up is the kind of motion that makes a page feel
  // busy rather than alive.
  var revealed = document.querySelectorAll('.reveal');
  if ('IntersectionObserver' in window && !reduced) {
    var io = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (en) {
          if (en.isIntersecting) {
            en.target.classList.add('on');
            io.unobserve(en.target);
          }
        });
      },
      { rootMargin: '0px 0px -8% 0px' }
    );
    revealed.forEach(function (el) {
      io.observe(el);
    });
  } else {
    revealed.forEach(function (el) {
      el.classList.add('on');
    });
  }
})();
