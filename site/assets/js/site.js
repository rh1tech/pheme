// pheme.rh1.tech — theme switch, language menu, mobile menu, reveal on scroll.
// No dependencies, no build step.
(function () {
  'use strict';

  var reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  var root = document.documentElement;

  // ---- theme ---------------------------------------------------------------------------------
  //
  // Three states, not two: light, dark, and "whatever the system says". A visitor who has never
  // touched the switch gets their OS preference and keeps getting it when they change it — the
  // stored value only exists once they have overruled it themselves.
  //
  // The initial class is set by an inline script in the <head> rather than here, so the page never
  // paints dark and then flips to light.
  var store = {
    get: function () {
      try { return localStorage.getItem('pheme.theme'); } catch (e) { return null; }
    },
    set: function (v) {
      try { localStorage.setItem('pheme.theme', v); } catch (e) { /* private mode; not fatal */ }
    },
  };

  var systemLight = window.matchMedia('(prefers-color-scheme: light)');
  var current = function () {
    return root.getAttribute('data-theme') || (systemLight.matches ? 'light' : 'dark');
  };

  var themeBtn = document.querySelector('[data-theme-toggle]');
  if (themeBtn) {
    var label = function () {
      themeBtn.setAttribute(
        'aria-label',
        themeBtn.getAttribute(current() === 'light' ? 'data-label-dark' : 'data-label-light') || 'Theme'
      );
    };
    label();
    themeBtn.addEventListener('click', function () {
      root.setAttribute('data-theme', current() === 'light' ? 'dark' : 'light');
      store.set(root.getAttribute('data-theme'));
      label();
    });
    // Follow the system while it has not been overruled.
    systemLight.addEventListener('change', function () {
      if (!store.get()) label();
    });
  }

  // ---- language menu -------------------------------------------------------------------------
  //
  // Built from the page's own <link rel="alternate" hreflang> tags. Every page already carries the
  // full set for SEO, so a new translation appears here the moment those links do — there is no
  // second list to keep in step, and no page that offers a language it does not have.
  var langMenu = document.querySelector('.lang-menu');
  if (langMenu) {
    var names = {
      en: 'English',
      ru: 'Русский',
    };
    var here = document.documentElement.lang || 'en';
    var list = document.createElement('ul');
    var seen = {};

    Array.prototype.forEach.call(
      document.querySelectorAll('link[rel="alternate"][hreflang]'),
      function (link) {
        var code = link.getAttribute('hreflang');
        // x-default duplicates a real language; listing it would offer the same page twice.
        if (code === 'x-default' || seen[code]) return;
        seen[code] = true;
        var li = document.createElement('li');
        var a = document.createElement('a');
        a.href = link.getAttribute('href');
        a.textContent = names[code] || code;
        a.setAttribute('lang', code);
        if (code === here) a.setAttribute('aria-current', 'true');
        li.appendChild(a);
        list.appendChild(li);
      }
    );

    // A menu offering one language is not a menu. Hide the control rather than show a dead end.
    if (list.children.length > 1) {
      langMenu.appendChild(list);
      var trigger = langMenu.querySelector('button');
      var setLangOpen = function (open) {
        if (open) langMenu.setAttribute('open', '');
        else langMenu.removeAttribute('open');
        if (trigger) trigger.setAttribute('aria-expanded', String(open));
      };
      if (trigger) {
        trigger.addEventListener('click', function (e) {
          e.stopPropagation();
          setLangOpen(!langMenu.hasAttribute('open'));
        });
      }
      document.addEventListener('click', function (e) {
        if (!langMenu.contains(e.target)) setLangOpen(false);
      });
      document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') setLangOpen(false);
      });
    } else {
      langMenu.hidden = true;
    }
  }

  // ---- mobile menu ---------------------------------------------------------------------------
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
    // Closes on a link, on a tap outside, and on Escape — all three, because a menu that only
    // closes one way is a menu somebody gets stuck in.
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

  // ---- reveal on scroll ----------------------------------------------------------------------
  // Unobserved once fired: this is a one-way transition, and re-animating on the way back up makes
  // a page feel busy rather than alive.
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
