package email

const (
    ActivationEmailTemplate = `
<!DOCTYPE html>
<html>
<head>
  <title>Email Activation</title>
  <style>
    .a_body {
      background-color: #25364D;
      color: #fff;
      font-family: Arial, sans-serif;
      margin: 0;
      padding: 0;
    }
    .container {
      max-width: 600px;
      margin: 0 auto;
      padding: 2rem;
      border: 1px solid #fff;
      border-radius: 10px;
      box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
    }
    .logo {
      text-align: center;
      margin-bottom: 1rem;
    }
    .logo img {
      max-width: 200px;
    }
  </style>
</head>
<body>
<div class="a_body">
  <div class="container">
    <div class="logo">
      <img src="https://www.prosecurelsp.com/images/logo.png" alt="LSP logo">
    </div>
    <div style="text-align: center;">
      <h2>Welcome to your plan</h2>
      <p>Hi %s!</p>
      <h3>Please Confirm Your Email Address</h3>
      <p>%s</p>
      <a href="%s" style="color:#fff; padding-bottom: 50px"><strong>Confirm Email</strong></a>
      <p>%s</p>
    </div>
  </div>
</div>
</body>
</html>`

    InvoiceEmailTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Receipt</title>
    <style>
        body { background-color: #f8f9fa; }
        .receipt-container {
            max-width: 600px;
            margin: 50px auto;
            padding: 20px;
            background-color: #ffffff;
            box-shadow: 0 0 10px rgba(0, 0, 0, 0.1);
        }
        table { width: 100%; border-collapse: collapse; }
        th, td { padding: 10px; border: 1px solid #dee2e6; }
        th { background-color: #007bff; color: #ffffff; }
    </style>
</head>
<body>
    <div class="receipt-container">
        <div style="background-color: #3b579d">
            <img src="https://prosecurelsp.com/images/logo.png" height="100" width="250"/>
        </div>
        <div>
            <h4>Invoice #%s</h4>
        </div>
        %s
        <div style="text-align: right">
            %s
        </div>
        <h3><strong>Status:</strong> %s</h3>
        <p>%s</p>
    </div>
</body>
</html>`
)