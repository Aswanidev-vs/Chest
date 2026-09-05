// Smooth scroll for internal links & active link tracking
document.addEventListener('DOMContentLoaded', () => {
  const links = document.querySelectorAll('.sidebar-links a');
  
  window.addEventListener('scroll', () => {
    let fromTop = window.scrollY + 100;

    links.forEach(link => {
      const section = document.querySelector(link.hash);
      if (
        section &&
        section.offsetTop <= fromTop &&
        section.offsetTop + section.offsetHeight > fromTop
      ) {
        links.forEach(l => l.classList.remove('active'));
        link.classList.add('active');
      }
    });
  });
});
